package discover

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/pdfrg/wmdl/internal/model"
)

type AOTYProvider struct {
	targetYear int
	targetWeek int
	hasTarget  bool
	client     *http.Client
}

func NewAOTYProvider() *AOTYProvider {
	return &AOTYProvider{
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (p *AOTYProvider) Name() string {
	return "albumoftheyear"
}

func (p *AOTYProvider) SetWeekRange(year, week int) {
	p.targetYear = year
	p.targetWeek = week
	p.hasTarget = true
}

func (p *AOTYProvider) Scrape() ([]ScrapedItem, error) {
	year, week := p.targetYear, p.targetWeek
	if !p.hasTarget {
		year, week = time.Now().ISOWeek()
	}

	// Compute the wmdl week (Wed-Tue) as a date range
	weekStart, weekEnd := wmdlWeekRange(year, week)

	var allItems []ScrapedItem
	page := 1

	for {
		items, err := p.scrapePage(page)
		if err != nil {
			return nil, fmt.Errorf("page %d: %w", page, err)
		}
		if len(items) == 0 {
			break
		}

		// Track whether any items on this page fall in our window
		var kept int
		var oldestDate time.Time

		for _, item := range items {
			itemDate, err := time.Parse("2006-01-02", item.ReleaseDate)
			if err != nil {
				continue
			}

			if oldestDate.IsZero() || itemDate.Before(oldestDate) {
				oldestDate = itemDate
			}

			if (itemDate.Equal(weekStart) || itemDate.After(weekStart)) &&
				(itemDate.Equal(weekEnd) || itemDate.Before(weekEnd) || itemDate.Equal(weekEnd)) {
				item.MediaType = model.MediaTypeMusic
				item.Source = "albumoftheyear"
				allItems = append(allItems, item)
				kept++
			}
		}

		// If all items on this page are older than our window, we're done
		if kept == 0 && !oldestDate.IsZero() && oldestDate.Before(weekStart) {
			break
		}

		page++

		// Safety limit
		if page > 20 {
			break
		}
	}

	return allItems, nil
}

func (p *AOTYProvider) scrapePage(page int) ([]ScrapedItem, error) {
	url := fmt.Sprintf("https://www.albumoftheyear.org/releases/%d/", page)
	if page <= 1 {
		url = "https://www.albumoftheyear.org/releases/"
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching page: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("page returned %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parsing html: %w", err)
	}

	var items []ScrapedItem

	doc.Find("div.albumBlock").Each(func(i int, s *goquery.Selection) {
		item := p.parseBlock(s)
		if item != nil {
			items = append(items, *item)
		}
	})

	return items, nil
}

func (p *AOTYProvider) parseBlock(s *goquery.Selection) *ScrapedItem {
	// Determine album type from data-type attribute
	albumTypeStr, _ := s.Attr("data-type")
	albumType := model.AlbumType(albumTypeStr)

	// Exclude types that don't integrate with Lidarr
	switch albumType {
	case model.AlbumTypeMixtape, model.AlbumTypeCompilation, model.AlbumTypeReissue, model.AlbumTypeSingle:
		return nil
	}

	artistName := strings.TrimSpace(s.Find("div.artistTitle").First().Text())
	albumTitle := strings.TrimSpace(s.Find("div.albumTitle").First().Text())
	if artistName == "" || albumTitle == "" {
		return nil
	}

	// Parse date and album sub-type from type div
	typeStr := strings.TrimSpace(s.Find("div.type").First().Text())
	releaseDate := parseAOTYDate(typeStr)

	// Parse scores
	var criticScore, userScore float64
	var criticCount, userCount int

	s.Find("div.ratingRow").Each(func(i int, row *goquery.Selection) {
		ratingText := strings.TrimSpace(row.Find("div.ratingText").First().Text())
		ratingStr := strings.TrimSpace(row.Find("div.rating").First().Text())
		if ratingStr == "" {
			return
		}

		score, err := strconv.ParseFloat(ratingStr, 64)
		if err != nil {
			return
		}

		// Extract count from parenthetical e.g. "(15)" or "(1,234)"
		countStr := ""
		row.Find("div.ratingText").Each(func(j int, rt *goquery.Selection) {
			t := strings.TrimSpace(rt.Text())
			if strings.HasPrefix(t, "(") {
				countStr = strings.Trim(t, "()")
				countStr = strings.ReplaceAll(countStr, ",", "")
			}
		})
		count, _ := strconv.Atoi(countStr)

		if strings.Contains(ratingText, "critic") {
			criticScore = score
			criticCount = count
		} else if strings.Contains(ratingText, "user") {
			userScore = score
			userCount = count
		}
	})

	mustHear := s.Find("div.mustHear, div.image.mustHear").Length() > 0

	// Extract year from date
	year := 0
	if t, err := time.Parse("2006-01-02", releaseDate); err == nil {
		year = t.Year()
	}

	return &ScrapedItem{
		Title:          albumTitle,
		Year:           year,
		ReleaseDate:    releaseDate,
		ArtistName:     artistName,
		AlbumType:      albumType,
		AOTYCriticScore: criticScore,
		AOTYCriticCount: criticCount,
		AOTYUserScore:   userScore,
		AOTYUserCount:   userCount,
		AOTYMustHear:    mustHear,
	}
}

// parseAOTYDate converts "May 29" or "May 29 • LP" to "2026-05-29"
func parseAOTYDate(s string) string {
	s = strings.Split(s, " • ")[0]
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	// Build a map of month names to numbers
	months := map[string]string{
		"january": "01", "february": "02", "march": "03", "april": "04",
		"may": "05", "june": "06", "july": "07", "august": "08",
		"september": "09", "october": "10", "november": "11", "december": "12",
		"jan": "01", "feb": "02", "mar": "03", "apr": "04",
		"jun": "06", "jul": "07", "aug": "08", "sep": "09",
		"oct": "10", "nov": "11", "dec": "12",
	}

	parts := strings.SplitN(s, " ", 2)
	if len(parts) != 2 {
		return ""
	}

	monthNum, ok := months[strings.ToLower(parts[0])]
	if !ok {
		return ""
	}

	day, err := strconv.Atoi(parts[1])
	if err != nil || day < 1 || day > 31 {
		return ""
	}

	now := time.Now()
	year := now.Year()

	// Build the date
	dateStr := fmt.Sprintf("%04d-%s-%02d", year, monthNum, day)
	if t, err := time.Parse("2006-01-02", dateStr); err == nil {
		// If the parsed date is more than 30 days in the future, it's probably
		// from a previous December being shown in January context
		if t.After(now.Add(30 * 24 * time.Hour)) {
			dateStr = fmt.Sprintf("%04d-%s-%02d", year-1, monthNum, day)
		}
		return dateStr
	}

	return ""
}

// wmdlWeekRange computes the Wed-to-Tue date range for an ISO week.
func wmdlWeekRange(year, week int) (start, end time.Time) {
	// Get the Tuesday (last day of wmdl week) from the ISO week
	// ISO week ends on Sunday, but wmdl week ends on Tuesday.
	// Tuesday of ISO week = Monday + 1.
	monday := isoWeekToDate(year, week)
	tuesday := monday.AddDate(0, 0, 1)

	// wmdl week: Wed to Tue. Wednesday = Tuesday - 6 days.
	wednesday := tuesday.AddDate(0, 0, -6)

	return wednesday, tuesday
}
