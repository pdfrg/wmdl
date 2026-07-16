package discover

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pdfrg/wmdl/internal/config"
	"github.com/pdfrg/wmdl/internal/model"
)

var _ ReleaseProvider = (*JikanAnimeProvider)(nil)
var _ WeekSettable = (*JikanAnimeProvider)(nil)

type JikanAnimeProvider struct {
	targetYear int
	targetWeek int
	hasTarget  bool
	cfg        config.AnimeConfig
	client     *http.Client
}

type jikanAnimeResponse struct {
	Pagination jikanPagination `json:"pagination"`
	Data       []jikanAnime    `json:"data"`
}

type jikanPagination struct {
	HasNextPage bool `json:"has_next_page"`
	Items       struct {
		Count int `json:"count"`
		Total int `json:"total"`
	} `json:"items"`
}

type jikanNamedItem struct {
	Name string `json:"name"`
}

type jikanAnime struct {
	MalID    int    `json:"mal_id"`
	Title    string `json:"title"`
	TitleEn  string `json:"title_english"`
	Type     string `json:"type"`
	Episodes int    `json:"episodes"`
	Status   string `json:"status"`
	Source   string `json:"source"`
	Aired    struct {
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"aired"`
	Score    float64 `json:"score"`
	ScoredBy int     `json:"scored_by"`
	Rank     int     `json:"rank"`
	Members  int     `json:"members"`
	Synopsis string  `json:"synopsis"`
	Rating   string  `json:"rating"`
	Season   string  `json:"season"`
	Year     int     `json:"year"`
	Images   struct {
		JPG struct {
			LargeImageURL string `json:"large_image_url"`
		} `json:"jpg"`
	} `json:"images"`
	Genres  []jikanNamedItem `json:"genres"`
	Studios []struct {
		Name string `json:"name"`
	} `json:"studios"`
	Themes       []jikanNamedItem `json:"themes"`
	Demographics []jikanNamedItem `json:"demographics"`
}

func NewJikanAnimeProvider(cfg config.AnimeConfig) *JikanAnimeProvider {
	return &JikanAnimeProvider{
		cfg:    cfg,
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (p *JikanAnimeProvider) Name() string {
	return "jikan"
}

func (p *JikanAnimeProvider) SetWeekRange(year, week int) {
	p.targetYear = year
	p.targetWeek = week
	p.hasTarget = true
}

func (p *JikanAnimeProvider) Scrape() ([]ScrapedItem, error) {
	year, week := p.targetYear, p.targetWeek
	if !p.hasTarget {
		year, week = time.Now().ISOWeek()
	}

	weekStart, weekEnd := wmdlWeekRange(year, week)

	var items []ScrapedItem

	phaseA, err := p.scrapePhaseA(weekStart, weekEnd)
	if err != nil {
		return nil, fmt.Errorf("phase A: %w", err)
	}
	items = append(items, phaseA...)

	if len(phaseA) < p.cfg.MinPhaseBResults && p.cfg.PhaseBEnabled {
		time.Sleep(time.Second)
		phaseB, err := p.scrapePhaseB()
		if err != nil {
			return nil, fmt.Errorf("phase B: %w", err)
		}
		items = append(items, phaseB...)
	}

	return items, nil
}

func (p *JikanAnimeProvider) scrapePhaseA(weekStart, weekEnd time.Time) ([]ScrapedItem, error) {
	var phaseA []ScrapedItem
	page := 1

	for {
		results, err := p.fetchPage(fmt.Sprintf(
			"https://api.jikan.moe/v4/anime?status=complete&type=TV&sfw=true&order_by=end_date&sort=desc&page=%d&limit=25", page))
		if err != nil {
			return nil, err
		}
		if len(results) == 0 {
			break
		}

		for _, a := range results {
			if a.Type != "TV" || a.Aired.To == "" {
				continue
			}

			endTime, err := parseJikanTime(a.Aired.To)
			if err != nil {
				continue
			}

			// Results are sorted by end_date descending. Once we're past
			// the wmdl week window, stop entirely.
			if endTime.Before(weekStart) {
				return phaseA, nil
			}

			if endTime.After(weekEnd) {
				continue
			}

			if a.Score <= 0 || a.Score < p.cfg.MinScore {
				continue
			}
			if a.Members < p.cfg.MinMembers {
				continue
			}

			phaseA = append(phaseA, p.toScrapedItem(a, "jikan"))
		}

		page++
		time.Sleep(time.Second)
	}

	return phaseA, nil
}

func (p *JikanAnimeProvider) scrapePhaseB() ([]ScrapedItem, error) {
	var phaseB []ScrapedItem
	page := 1

	for {
		results, err := p.fetchPage(fmt.Sprintf(
			"https://api.jikan.moe/v4/anime?status=airing&type=TV&sfw=true&order_by=score&sort=desc&page=%d&limit=25", page))
		if err != nil {
			return nil, err
		}
		if len(results) == 0 {
			break
		}

		for _, a := range results {
			if a.Type != "TV" {
				continue
			}
			if a.Score <= 0 {
				continue
			}
			// Sorted by score descending — once below threshold, skip rest of page
			// but continue to next page (same-score items may span pages).
			if a.Score < p.cfg.PhaseBMinScore {
				break
			}
			if a.Members < p.cfg.PhaseBMinMembers {
				continue
			}

			phaseB = append(phaseB, p.toScrapedItemB(a))
		}

		if len(results) < 25 {
			break
		}
		page++
		time.Sleep(time.Second)
	}

	return phaseB, nil
}

func (p *JikanAnimeProvider) fetchPage(url string) ([]jikanAnime, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			delay := time.Duration(1<<attempt) * time.Second
			time.Sleep(delay)
		}

		resp, err := p.client.Get(url)
		if err != nil {
			lastErr = fmt.Errorf("fetching %s: %w", url, err)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("reading response: %w", err)
			continue
		}

		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("jikan returned %d: %s", resp.StatusCode, string(body[:min(len(body), 200)]))
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("jikan returned %d: %s", resp.StatusCode, string(body[:min(len(body), 200)]))
		}

		var result jikanAnimeResponse
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("decoding response: %w", err)
		}

		return result.Data, nil
	}

	return nil, lastErr
}

func joinJikanNames(items []jikanNamedItem) string {
	if len(items) == 0 {
		return ""
	}
	names := make([]string, len(items))
	for i, it := range items {
		names[i] = it.Name
	}
	return strings.Join(names, ", ")
}

func (p *JikanAnimeProvider) toScrapedItem(a jikanAnime, source string) ScrapedItem {
	title := a.Title
	if a.TitleEn != "" {
		title = a.TitleEn
	}

	studio := ""
	if len(a.Studios) > 0 {
		studio = a.Studios[0].Name
	}

	item := ScrapedItem{
		Title:         title,
		MediaType:     model.MediaTypeAnime,
		ReleaseType:   model.ReleaseStreaming,
		Source:        source,
		MalID:         a.MalID,
		ImageURL:      a.Images.JPG.LargeImageURL,
		Overview:      a.Synopsis,
		ImdbRating:    a.Score,
		USRating:      a.Rating,
		AnimeType:     a.Type,
		AnimeEpisodes: a.Episodes,
		AnimeStatus:   a.Status,
		AnimeMembers:  a.Members,
		AnimeRank:     a.Rank,
		AnimeSource:   a.Source,
		AnimeStudio:   studio,
		Genres:        joinJikanNames(a.Genres),
		Themes:        joinJikanNames(a.Themes),
		Demographics:  joinJikanNames(a.Demographics),
	}

	if a.Year > 0 {
		item.Year = a.Year
	} else {
		if t, err := parseJikanTime(a.Aired.From); err == nil {
			item.Year = t.Year()
		}
	}

	return item
}

func (p *JikanAnimeProvider) toScrapedItemB(a jikanAnime) ScrapedItem {
	item := p.toScrapedItem(a, "jikan-airing")

	var endDate string
	if a.Aired.To != "" {
		if t, err := parseJikanTime(a.Aired.To); err == nil {
			endDate = t.Format("2006-01-02")
		}
	}

	epStr := ""
	if a.Episodes > 0 {
		epStr = strconv.Itoa(a.Episodes)
	}

	item.Notes = fmt.Sprintf("airing|end=%s|eps=%s", endDate, epStr)
	return item
}

func parseJikanTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err == nil {
		return t, nil
	}
	t, err = time.Parse("2006-01-02T00:00:00+00:00", s)
	if err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", s)
}
