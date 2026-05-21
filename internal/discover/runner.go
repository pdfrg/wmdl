package discover

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/pdfrg/wmd/internal/config"
	"github.com/pdfrg/wmd/internal/db"
	"github.com/pdfrg/wmd/internal/model"
	"github.com/pdfrg/wmd/internal/notifier"
)

type Runner struct {
	cfg    *config.Config
	db     *db.DB
	tmdb   *TMDBClient
	rt     *RTFinder
	notify *notifier.Gotify
}

func NewRunner(cfg *config.Config, database *db.DB) *Runner {
	return &Runner{
		cfg:    cfg,
		db:     database,
		tmdb:   NewTMDBClient(cfg.TMDB.APIKey, cfg.TMDB.AccessToken),
		rt:     NewRTFinder(),
		notify: notifier.NewGotify(cfg.Notifier.GotifyURL, cfg.Notifier.GotifyToken),
	}
}

func (r *Runner) Run(ctx context.Context) error {
	providers := []ReleaseProvider{
		NewDVDReleaseDates(),
		NewTMDBStreamingProvider(r.tmdb),
	}

	var allItems []ScrapedItem

	for _, p := range providers {
		log.Printf("Scraping %s...", p.Name())
		items, err := p.Scrape()
		if err != nil {
			log.Printf("Warning: %s failed: %v", p.Name(), err)
			continue
		}
		log.Printf("  Found %d items from %s", len(items), p.Name())
		allItems = append(allItems, items...)
	}

	if len(allItems) == 0 {
		log.Println("No new releases found.")
		return nil
	}

	// Deduplicate by title+year
	seen := make(map[string]bool)
	var unique []ScrapedItem
	for _, item := range allItems {
		key := fmt.Sprintf("%s|%d|%s", item.Title, item.Year, item.ReleaseType)
		if seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, item)
	}

	var processed int
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 3) // limit concurrency

	for _, item := range unique {
		wg.Add(1)
		go func(item ScrapedItem) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if err := r.processItem(ctx, item); err != nil {
				log.Printf("  Error processing %q: %v", item.Title, err)
				return
			}
			mu.Lock()
			processed++
			mu.Unlock()
		}(item)
	}

	wg.Wait()
	log.Printf("Processed %d/%d items", processed, len(unique))

	// Send notification
	if r.notify != nil && processed > 0 {
		msg := fmt.Sprintf("**%d new release%s** ready for review:\n", processed, map[bool]string{true: "s", false: ""}[processed != 1])
		count := 0
		for _, item := range unique {
			if count >= 5 {
				msg += fmt.Sprintf("\n+ %d more", processed-count)
				break
			}
			msg += fmt.Sprintf("\n- %s (%d)", item.Title, item.Year)
			count++
		}
		if err := r.notify.Send("wmd: New Releases", msg, 5); err != nil {
			log.Printf("Warning: notification failed: %v", err)
		}
	}

	return nil
}

func (r *Runner) processItem(ctx context.Context, item ScrapedItem) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	mediaType := item.MediaType

	// Enrich with TMDB
	var tmdbID int
	var rating float64

	enrich, err := r.tmdb.Enrich(ctx, item.Title, item.Year)
	if err == nil {
		mediaType = model.MediaType(enrich.MediaType)
		tmdbID = enrich.TMDBID
		rating = enrich.Rating
	} else {
		log.Printf("  TMDB lookup failed for %q: %v", item.Title, err)
	}

	// Get RT URL
	rtURL := r.rt.FindURL(item.Title, item.Year, string(mediaType))

	// Upsert title
	title := &model.Title{
		TmdbID:     tmdbID,
		Title:      item.Title,
		Year:       item.Year,
		MediaType:  mediaType,
		TmdbRating: rating,
		RTURL:      rtURL,
	}

	titleID, err := r.db.UpsertTitle(ctx, title)
	if err != nil {
		return fmt.Errorf("saving title: %w", err)
	}

	// Check for existing release events
	existing, err := r.db.GetLatestReleaseEvent(ctx, titleID)
	if err != nil {
		return fmt.Errorf("checking existing events: %w", err)
	}

	var prevStatus model.ReleaseStatus
	if existing != nil {
		prevStatus = existing.Status
	}

	// Determine new status
	status := model.StatusPending
	if existing != nil {
		switch existing.Status {
		case model.StatusRejected:
			// Previously rejected — still show with note
			status = model.StatusPending
		case model.StatusDownloaded:
			// Previously downloaded — pending for upgrade consideration
			status = model.StatusPending
		}
	}

	event := &model.ReleaseEvent{
		TitleID:        titleID,
		Source:         "scraper",
		ReleaseType:    item.ReleaseType,
		ReleaseDate:    item.ReleaseDate,
		Status:         status,
		PreviousStatus: prevStatus,
	}

	if _, err := r.db.CreateReleaseEvent(ctx, event); err != nil {
		return fmt.Errorf("saving release event: %w", err)
	}

	return nil
}
