package discover

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/pdfrg/wmdl/internal/config"
	"github.com/pdfrg/wmdl/internal/model"
)

var _ ReleaseProvider = (*AniListProvider)(nil)
var _ WeekSettable = (*AniListProvider)(nil)

type AniListProvider struct {
	targetYear int
	targetWeek int
	hasTarget  bool
	cfg        config.AnimeConfig
	client     *http.Client
}

var anilistPhaseAQuery = `query ($page: Int, $endGt: FuzzyDateInt, $endLt: FuzzyDateInt) {
  Page(page: $page, perPage: 25) {
    pageInfo { hasNextPage }
    media(type: ANIME, status: FINISHED, format_in: [TV, TV_SHORT],
          endDate_greater: $endGt, endDate_lesser: $endLt,
          sort: [END_DATE_DESC, SCORE_DESC]) {
      id idMal title { romaji english }
      format status episodes averageScore meanScore popularity genres source
      startDate { year month day }
      endDate { year month day }
      coverImage { large }
      studios { nodes { name } }
      description(asHtml: false)
    }
  }
}`

var anilistPhaseBQuery = `query ($page: Int) {
  Page(page: $page, perPage: 25) {
    pageInfo { hasNextPage }
    media(type: ANIME, status: RELEASING, format_in: [TV, TV_SHORT],
          sort: [SCORE_DESC, POPULARITY_DESC]) {
      id idMal title { romaji english }
      format status episodes averageScore popularity genres
      startDate { year month day }
      coverImage { large }
      studios { nodes { name } }
    }
  }
}`

type anilistGraphQLResponse struct {
	Data   anilistPageData `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors,omitempty"`
}

type anilistPageData struct {
	Page anilistPage `json:"Page"`
}

type anilistPage struct {
	PageInfo anilistPageInfo `json:"pageInfo"`
	Media    []anilistMedia  `json:"media"`
}

type anilistPageInfo struct {
	HasNextPage bool `json:"hasNextPage"`
}

type anilistMedia struct {
	ID    int `json:"id"`
	IDMal int `json:"idMal"`
	Title struct {
		Romaji  string `json:"romaji"`
		English string `json:"english"`
	} `json:"title"`
	Format       string           `json:"format"`
	Status       string           `json:"status"`
	Episodes     int              `json:"episodes"`
	AverageScore int              `json:"averageScore"`
	MeanScore    int              `json:"meanScore"`
	Popularity   int              `json:"popularity"`
	Genres       []string         `json:"genres"`
	Source       string           `json:"source"`
	StartDate    anilistFuzzyDate `json:"startDate"`
	EndDate      anilistFuzzyDate `json:"endDate"`
	CoverImage   struct {
		Large string `json:"large"`
	} `json:"coverImage"`
	Studios     anilistStudioConnection `json:"studios"`
	Description string                  `json:"description"`
}

type anilistFuzzyDate struct {
	Year  int `json:"year"`
	Month int `json:"month"`
	Day   int `json:"day"`
}

type anilistStudioConnection struct {
	Nodes []struct {
		Name string `json:"name"`
	} `json:"nodes"`
}

var anilistStripRe = regexp.MustCompile(`<[^>]*>`)

type anilistGraphQLRequest struct {
	Query     string `json:"query"`
	Variables any    `json:"variables"`
}

func NewAniListProvider(cfg config.AnimeConfig) *AniListProvider {
	return &AniListProvider{
		cfg:    cfg,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (p *AniListProvider) Name() string {
	return "anilist"
}

func (p *AniListProvider) SetWeekRange(year, week int) {
	p.targetYear = year
	p.targetWeek = week
	p.hasTarget = true
}

func (p *AniListProvider) Scrape() ([]ScrapedItem, error) {
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

func (p *AniListProvider) scrapePhaseA(weekStart, weekEnd time.Time) ([]ScrapedItem, error) {
	endGt := fuzzyDateInt(weekStart)
	endLt := fuzzyDateInt(weekEnd)

	var phaseA []ScrapedItem
	page := 1

	for {
		media, hasNext, err := p.fetchPage(anilistPhaseAQuery, map[string]any{
			"page":  page,
			"endGt": endGt,
			"endLt": endLt,
		})
		if err != nil {
			return nil, err
		}
		if len(media) == 0 {
			break
		}

		for _, m := range media {
			if m.AverageScore <= 0 || float64(m.AverageScore)/10 < p.cfg.MinScore {
				continue
			}
			if m.Popularity < p.cfg.MinMembers {
				continue
			}

			phaseA = append(phaseA, p.toScrapedItem(m, "anilist"))
		}

		if !hasNext {
			break
		}
		page++
		time.Sleep(time.Second)
	}

	return phaseA, nil
}

func (p *AniListProvider) scrapePhaseB() ([]ScrapedItem, error) {
	// Only fetch the first page — sorted by SCORE_DESC, POPULARITY_DESC so
	// we always get the top 25 highest-scored currently-airing shows.
	// Users tune quantity via PhaseBMinScore / PhaseBMinMembers.
	media, _, err := p.fetchPage(anilistPhaseBQuery, map[string]any{
		"page": 1,
	})
	if err != nil {
		return nil, err
	}

	var phaseB []ScrapedItem
	for _, m := range media {
		if m.AverageScore <= 0 {
			continue
		}
		minScore := int(p.cfg.PhaseBMinScore * 10)
		if m.AverageScore < minScore {
			break
		}
		if m.Popularity < p.cfg.PhaseBMinMembers {
			continue
		}

		phaseB = append(phaseB, p.toScrapedItem(m, "anilist-airing"))
	}

	return phaseB, nil
}

func (p *AniListProvider) fetchPage(query string, vars map[string]any) ([]anilistMedia, bool, error) {
	reqBody := anilistGraphQLRequest{Query: query, Variables: vars}
	bodyJSON, err := json.Marshal(reqBody)
	if err != nil {
		return nil, false, fmt.Errorf("marshalling request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, "https://graphql.anilist.co",
		bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, false, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("fetching: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("anilist returned %d: %s",
			resp.StatusCode, string(body[:min(len(body), 300)]))
	}

	var gqlResp anilistGraphQLResponse
	if err := json.Unmarshal(body, &gqlResp); err != nil {
		return nil, false, fmt.Errorf("decoding response: %w", err)
	}

	if len(gqlResp.Errors) > 0 {
		return nil, false, fmt.Errorf("anilist error: %s", gqlResp.Errors[0].Message)
	}

	return gqlResp.Data.Page.Media, gqlResp.Data.Page.PageInfo.HasNextPage, nil
}

func (d anilistFuzzyDate) IsZero() bool {
	return d.Year == 0 && d.Month == 0 && d.Day == 0
}

func (d anilistFuzzyDate) ToTime() time.Time {
	if d.IsZero() {
		return time.Time{}
	}
	return time.Date(d.Year, time.Month(d.Month), d.Day, 0, 0, 0, 0, time.UTC)
}

func fuzzyDateInt(t time.Time) int {
	return t.Year()*10000 + int(t.Month())*100 + t.Day()
}

func (p *AniListProvider) toScrapedItem(m anilistMedia, source string) ScrapedItem {
	title := m.Title.English
	if title == "" {
		title = m.Title.Romaji
	}

	studio := ""
	if len(m.Studios.Nodes) > 0 {
		studio = m.Studios.Nodes[0].Name
	}

	imdbRating := float64(m.AverageScore) / 10

	genres := strings.Join(m.Genres, ", ")

	overview := anilistStripRe.ReplaceAllString(m.Description, "")
	overview = htmlEntityUnescape(overview)
	overview = strings.TrimSpace(overview)

	year := m.StartDate.Year
	if year == 0 && !m.EndDate.IsZero() {
		year = m.EndDate.Year
	}

	item := ScrapedItem{
		Title:         title,
		Year:          year,
		MediaType:     model.MediaTypeAnime,
		ReleaseType:   model.ReleaseStreaming,
		Source:        source,
		MalID:         m.IDMal,
		ImageURL:      m.CoverImage.Large,
		Overview:      overview,
		ImdbRating:    imdbRating,
		AnimeType:     m.Format,
		AnimeEpisodes: m.Episodes,
		AnimeStatus:   m.Status,
		AnimeMembers:  m.Popularity,
		AnimeSource:   m.Source,
		AnimeStudio:   studio,
		Genres:        genres,
	}

	if source == "anilist-airing" {
		var endDate string
		if !m.EndDate.IsZero() {
			endDate = m.EndDate.ToTime().Format("2006-01-02")
		}
		epStr := ""
		if m.Episodes > 0 {
			epStr = fmt.Sprintf("%d", m.Episodes)
		}
		item.Notes = fmt.Sprintf("airing|end=%s|eps=%s", endDate, epStr)
	}

	return item
}

func htmlEntityUnescape(s string) string {
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", "\"")
	s = strings.ReplaceAll(s, "&#39;", "'")
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	return s
}
