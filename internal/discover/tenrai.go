package discover

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/pdfrg/wmdl/internal/config"
	"github.com/pdfrg/wmdl/internal/model"
)

var _ ReleaseProvider = (*TenraiAnimeProvider)(nil)
var _ WeekSettable = (*TenraiAnimeProvider)(nil)

const tenraiBaseURL = "https://api.tenrai.org/v1"

type TenraiAnimeProvider struct {
	targetYear int
	targetWeek int
	hasTarget  bool
	cfg        config.AnimeConfig
	client     *http.Client
	limiter    *rate.Limiter
}

type tenraiAnimeResponse struct {
	Pagination tenraiPagination `json:"pagination"`
	Data       []tenraiAnime    `json:"data"`
}

type tenraiPagination struct {
	HasNextPage bool `json:"has_next_page"`
	Items       struct {
		Count int `json:"count"`
		Total int `json:"total"`
	} `json:"items"`
}

type tenraiNamedItem struct {
	Name string `json:"name"`
}

type tenraiAnime struct {
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
	Genres  []tenraiNamedItem `json:"genres"`
	Studios []struct {
		Name string `json:"name"`
	} `json:"studios"`
	Themes       []tenraiNamedItem `json:"themes"`
	Demographics []tenraiNamedItem `json:"demographics"`
}

func NewTenraiAnimeProvider(cfg config.AnimeConfig) *TenraiAnimeProvider {
	return &TenraiAnimeProvider{
		cfg:     cfg,
		client:  &http.Client{Timeout: 15 * time.Second},
		limiter: rate.NewLimiter(rate.Limit(1), 3),
	}
}

func (p *TenraiAnimeProvider) Name() string {
	return "tenrai"
}

func (p *TenraiAnimeProvider) SetWeekRange(year, week int) {
	p.targetYear = year
	p.targetWeek = week
	p.hasTarget = true
}

func (p *TenraiAnimeProvider) Scrape() ([]ScrapedItem, error) {
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
		if err := p.limiter.Wait(context.Background()); err != nil {
			return nil, fmt.Errorf("rate limit: %w", err)
		}
		phaseB, err := p.scrapePhaseB()
		if err != nil {
			return nil, fmt.Errorf("phase B: %w", err)
		}
		items = append(items, phaseB...)
	}

	return items, nil
}

func (p *TenraiAnimeProvider) scrapePhaseA(weekStart, weekEnd time.Time) ([]ScrapedItem, error) {
	var phaseA []ScrapedItem
	page := 1

	for {
		results, err := p.fetchPage(fmt.Sprintf(
			"%s/anime?status=complete&type=TV&sfw=true&order_by=end_date&sort=desc&page=%d&limit=25", tenraiBaseURL, page))
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

			phaseA = append(phaseA, p.toScrapedItem(a, "tenrai"))
		}

		page++
		if err := p.limiter.Wait(context.Background()); err != nil {
			return nil, fmt.Errorf("rate limit: %w", err)
		}
	}

	return phaseA, nil
}

func (p *TenraiAnimeProvider) scrapePhaseB() ([]ScrapedItem, error) {
	results, err := p.fetchPage(fmt.Sprintf(
		"%s/anime?status=airing&type=TV&sfw=true&order_by=score&sort=desc&page=1&limit=25", tenraiBaseURL))
	if err != nil {
		return nil, err
	}

	var phaseB []ScrapedItem
	for _, a := range results {
		if a.Type != "TV" {
			continue
		}
		if a.Score <= 0 {
			continue
		}
		if a.Score < p.cfg.PhaseBMinScore {
			break
		}
		if a.Members < p.cfg.PhaseBMinMembers {
			continue
		}

		phaseB = append(phaseB, p.toScrapedItemB(a))
	}

	return phaseB, nil
}

func (p *TenraiAnimeProvider) fetchPage(url string) ([]tenraiAnime, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := p.limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			delay := time.Duration(1<<attempt) * time.Second
			time.Sleep(delay)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			lastErr = fmt.Errorf("creating request: %w", err)
			continue
		}

		resp, err := p.client.Do(req)
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
			lastErr = newAPIStatusError("tenrai", resp.StatusCode, body)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return nil, newAPIStatusError("tenrai", resp.StatusCode, body)
		}

		var result tenraiAnimeResponse
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("decoding response: %w", err)
		}

		return result.Data, nil
	}

	return nil, lastErr
}

func joinTenraiNames(items []tenraiNamedItem) string {
	if len(items) == 0 {
		return ""
	}
	names := make([]string, len(items))
	for i, it := range items {
		names[i] = it.Name
	}
	return strings.Join(names, ", ")
}

func (p *TenraiAnimeProvider) toScrapedItem(a tenraiAnime, source string) ScrapedItem {
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
		Genres:        joinTenraiNames(a.Genres),
		Themes:        joinTenraiNames(a.Themes),
		Demographics:  joinTenraiNames(a.Demographics),
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

func (p *TenraiAnimeProvider) toScrapedItemB(a tenraiAnime) ScrapedItem {
	item := p.toScrapedItem(a, "tenrai-airing")

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
