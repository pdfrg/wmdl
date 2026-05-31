package discover

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
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

type jikianimeResponse struct {
	Pagination jikanPagination `json:"pagination"`
	Data       []jikanAnime    `json:"data"`
}

type jikanPagination struct {
	LastVisiblePage int  `json:"last_visible_page"`
	HasNextPage     bool `json:"has_next_page"`
	CurrentPage     int  `json:"current_page"`
	Items           struct {
		Count   int `json:"count"`
		Total   int `json:"total"`
		PerPage int `json:"per_page"`
	} `json:"items"`
}

type jikanAnime struct {
	MalID    int    `json:"mal_id"`
	URL      string `json:"url"`
	Title    string `json:"title"`
	TitleEn  string `json:"title_english"`
	Type     string `json:"type"`
	Source   string `json:"source"`
	Episodes int    `json:"episodes"`
	Status   string `json:"status"`
	Aired    struct {
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"aired"`
	Score      float64 `json:"score"`
	ScoredBy   int     `json:"scored_by"`
	Rank       int     `json:"rank"`
	Popularity int     `json:"popularity"`
	Members    int     `json:"members"`
	Favorites  int     `json:"favorites"`
	Synopsis   string  `json:"synopsis"`
	Rating     string  `json:"rating"`
	Season     string  `json:"season"`
	Year       int     `json:"year"`
	Images     struct {
		JPG struct {
			LargeImageURL string `json:"large_image_url"`
		} `json:"jpg"`
	} `json:"images"`
	Genres []struct {
		ID   int    `json:"mal_id"`
		Name string `json:"name"`
	} `json:"genres"`
	Studios []struct {
		ID   int    `json:"mal_id"`
		Name string `json:"name"`
	} `json:"studios"`
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
	if !p.hasTarget {
		weekEnd = time.Now()
	}

	seasonYear, seasonName := currentSeasonEnded()
	if seasonName == "" {
		return nil, nil
	}

	animeList, err := p.fetchSeason(seasonYear, seasonName)
	if err != nil {
		return nil, fmt.Errorf("fetching jikan season: %w", err)
	}

	var phaseA []jikanAnime
	var phaseB []jikanAnime

	for _, a := range animeList {
		if a.Type != "TV" {
			continue
		}
		if a.Score <= 0 || a.Score < p.cfg.MinScore {
			continue
		}

		if a.Status == "Finished Airing" && a.Aired.To != "" {
			endTime, err := time.Parse(time.RFC3339, a.Aired.To)
			if err != nil {
				endTime, err = time.Parse("2006-01-02T00:00:00+00:00", a.Aired.To)
				if err != nil {
					continue
				}
			}
			if inDateRange(endTime, weekStart, weekEnd) || inDateRange(endTime, weekStart.AddDate(0, 0, -3), weekEnd) {
				if a.Members >= p.cfg.MinMembers {
					phaseA = append(phaseA, a)
				}
			}
		}
	}

	if len(phaseA) < p.cfg.MinPhaseBResults && p.cfg.PhaseBEnabled {
		for _, a := range animeList {
			if a.Type != "TV" {
				continue
			}
			if a.Score <= 0 {
				continue
			}

			if a.Status == "Currently Airing" {
				if a.Score >= p.cfg.PhaseBMinScore && a.Members >= p.cfg.PhaseBMinMembers {
					phaseB = append(phaseB, a)
				}
			}
		}
	}

	var items []ScrapedItem
	for _, a := range phaseA {
		items = append(items, p.toScrapedItem(a, "jikan"))
	}
	for _, a := range phaseB {
		items = append(items, p.toScrapedItemB(a))
	}

	return items, nil
}

func (p *JikanAnimeProvider) fetchSeason(year int, season string) ([]jikanAnime, error) {
	var all []jikanAnime
	page := 1

	for {
		u := fmt.Sprintf("https://api.jikan.moe/v4/seasons/%d/%s?sfw=true&page=%d&limit=25", year, season, page)
		resp, err := p.client.Get(u)
		if err != nil {
			return nil, fmt.Errorf("fetching page %d: %w", page, err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("reading page %d: %w", page, err)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("jikan returned %d for page %d: %s", resp.StatusCode, page, string(body[:min(len(body), 200)]))
		}

		var result jikianimeResponse
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("decoding page %d: %w", page, err)
		}

		all = append(all, result.Data...)

		if !result.Pagination.HasNextPage {
			break
		}
		page++
		time.Sleep(400 * time.Millisecond)
	}

	return all, nil
}

func (p *JikanAnimeProvider) toScrapedItem(a jikanAnime, source string) ScrapedItem {
	item := ScrapedItem{
		Title:       a.Title,
		Year:        a.Year,
		MediaType:   model.MediaTypeAnime,
		ReleaseType: model.ReleaseStreaming,
		Source:      source,
		MalID:       a.MalID,
		ImageURL:    a.Images.JPG.LargeImageURL,
		Overview:    a.Synopsis,
		ImdbRating:  a.Score,
	}

	if a.Year <= 0 {
		item.Year = extractYearFromAired(a.Aired.From)
	}

	return item
}

func (p *JikanAnimeProvider) toScrapedItemB(a jikanAnime) ScrapedItem {
	item := p.toScrapedItem(a, "jikan-airing")

	var endDate string
	if a.Aired.To != "" {
		if t, err := time.Parse(time.RFC3339, a.Aired.To); err == nil {
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

func currentSeasonEnded() (int, string) {
	now := time.Now()
	year := now.Year()
	month := now.Month()

	switch {
	case month >= 1 && month <= 3:
		return year - 1, "fall"
	case month >= 4 && month <= 6:
		return year, "winter"
	case month >= 7 && month <= 9:
		return year, "spring"
	default:
		return year, "summer"
	}
}

func extractYearFromAired(airedFrom string) int {
	if airedFrom == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339, airedFrom)
	if err != nil {
		t, err = time.Parse("2006-01-02T00:00:00+00:00", airedFrom)
		if err != nil {
			return 0
		}
	}
	return t.Year()
}

func inDateRange(t, start, end time.Time) bool {
	return (t.Equal(start) || t.After(start)) && (t.Equal(end) || t.Before(end))
}
