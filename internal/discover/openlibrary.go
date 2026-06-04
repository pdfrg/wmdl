package discover

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type OLClient struct {
	http *http.Client
}

type OLBookResult struct {
	OLID        string
	Title       string
	Subtitle    string
	Description string
	Pages       int
	ReleaseDate string
	ReleaseYear int
	ImageURL    string
	ISBN10      string
	ISBN13      string
	Subjects    []string

	Author *OLAuthorResult
}

type OLAuthorResult struct {
	OLID      string
	Name      string
	Bio       string
	BornDate  string
	DeathDate string
	ImageURL  string
}

func NewOLClient() *OLClient {
	return &OLClient{
		http: &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:    2,
				IdleConnTimeout: 30 * time.Second,
			},
		},
	}
}

func (c *OLClient) SearchBook(ctx context.Context, title, author string) (*OLBookResult, error) {
	q := url.Values{}
	q.Set("title", title)
	q.Set("author", author)
	q.Set("limit", "5")
	q.Set("fields", "key,title,subtitle,first_publish_year,publish_year,publish_date,author_name,author_key,subject,isbn,ia,cover_i")

	u := "https://openlibrary.org/search.json?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	req.Header.Set("User-Agent", "wmdl/1.0 (books@wmdl)")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openlibrary api: status=%d", resp.StatusCode)
	}

	var searchResp struct {
		Docs []json.RawMessage `json:"docs"`
	}
	if err := json.Unmarshal(body, &searchResp); err != nil {
		return nil, fmt.Errorf("unmarshal search: %w", err)
	}

	if len(searchResp.Docs) == 0 {
		return nil, nil
	}

	// Try to find the best match by ISBN or exact title match
	bestDoc := searchResp.Docs[0]
	bestScore := 0

	for _, doc := range searchResp.Docs {
		var d struct {
			Key              string   `json:"key"`
			Title            string   `json:"title"`
			FirstPublishYear int      `json:"first_publish_year"`
			AuthorName       []string `json:"author_name"`
			ISBN             []string `json:"isbn"`
			Subject          []string `json:"subject"`
			IA               []string `json:"ia"`
		}
		if err := json.Unmarshal(doc, &d); err != nil {
			continue
		}

		score := 0
		if containsFold(d.Title, title) || containsFold(title, d.Title) {
			score += 10
		}
		for _, an := range d.AuthorName {
			if containsFold(an, author) || containsFold(author, an) {
				score += 5
				break
			}
		}
		if score > bestScore {
			bestScore = score
			bestDoc = doc
		}
	}

	return c.decodeSearchResult(bestDoc)
}

func (c *OLClient) decodeSearchResult(raw json.RawMessage) (*OLBookResult, error) {
	var d struct {
		Key              string   `json:"key"`
		Title            string   `json:"title"`
		Subtitle         string   `json:"subtitle"`
		FirstPublishYear int      `json:"first_publish_year"`
		PublishYear      []int    `json:"publish_year"`
		PublishDate      []string `json:"publish_date"`
		AuthorName       []string `json:"author_name"`
		AuthorKey        []string `json:"author_key"`
		ISBN             []string `json:"isbn"`
		Subject          []string `json:"subject"`
		IA               []string `json:"ia"`
		CoverI           int      `json:"cover_i"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("decode doc: %w", err)
	}

	releaseYear := d.FirstPublishYear
	if len(d.PublishYear) > 0 {
		maxYear := 0
		for _, y := range d.PublishYear {
			if y > maxYear {
				maxYear = y
			}
		}
		if maxYear > 0 {
			releaseYear = maxYear
		}
	}

	releaseDate := ""
	if len(d.PublishDate) > 0 {
		releaseDate = d.PublishDate[len(d.PublishDate)-1]
	}

	res := &OLBookResult{
		OLID:        d.Key,
		Title:       d.Title,
		Subtitle:    d.Subtitle,
		ReleaseDate: releaseDate,
		ReleaseYear: releaseYear,
		Subjects:    d.Subject,
	}

	for _, isbn := range d.ISBN {
		if len(isbn) == 10 && res.ISBN10 == "" {
			res.ISBN10 = isbn
		}
		if len(isbn) == 13 && res.ISBN13 == "" {
			res.ISBN13 = isbn
		}
	}

	if d.CoverI > 0 {
		res.ImageURL = fmt.Sprintf("https://covers.openlibrary.org/b/id/%d-M.jpg", d.CoverI)
	}

	if len(d.AuthorName) > 0 {
		authorOLID := ""
		if len(d.AuthorKey) > 0 {
			authorOLID = d.AuthorKey[0]
		}
		res.Author = &OLAuthorResult{
			Name: d.AuthorName[0],
			OLID: authorOLID,
		}
	}

	return res, nil
}

func (c *OLClient) GetBook(ctx context.Context, olid string) (*OLBookResult, error) {
	u := fmt.Sprintf("https://openlibrary.org%s.json", olid)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	req.Header.Set("User-Agent", "wmdl/1.0 (books@wmdl)")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openlibrary: status=%d", resp.StatusCode)
	}

	var work struct {
		Title       string          `json:"title"`
		Description json.RawMessage `json:"description"`
		Covers      []int           `json:"covers"`
		Subjects    []string        `json:"subjects"`
		Created     struct {
			Value string `json:"value"`
		} `json:"created"`
	}
	if err := json.Unmarshal(body, &work); err != nil {
		return nil, fmt.Errorf("unmarshal work: %w", err)
	}

	res := &OLBookResult{
		OLID:     olid,
		Title:    work.Title,
		Subjects: work.Subjects,
	}

	if work.Description != nil {
		var descStr string
		if json.Unmarshal(work.Description, &descStr) == nil {
			res.Description = descStr
		} else {
			var descObj struct {
				Value string `json:"value"`
			}
			if json.Unmarshal(work.Description, &descObj) == nil {
				res.Description = descObj.Value
			}
		}
	}

	if len(work.Covers) > 0 {
		res.ImageURL = fmt.Sprintf("https://covers.openlibrary.org/b/id/%d-M.jpg", work.Covers[0])
	}

	return res, nil
}

func (c *OLClient) GetAuthor(ctx context.Context, olid string) (*OLAuthorResult, error) {
	u := fmt.Sprintf("https://openlibrary.org%s.json", olid)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	req.Header.Set("User-Agent", "wmdl/1.0 (books@wmdl)")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openlibrary: status=%d", resp.StatusCode)
	}

	var a struct {
		Name      string          `json:"name"`
		Bio       json.RawMessage `json:"bio"`
		BirthDate string          `json:"birth_date"`
		DeathDate string          `json:"death_date"`
		Photos    []int           `json:"photos"`
		Key       string          `json:"key"`
	}
	if err := json.Unmarshal(body, &a); err != nil {
		return nil, fmt.Errorf("unmarshal author: %w", err)
	}

	res := &OLAuthorResult{
		OLID:      a.Key,
		Name:      a.Name,
		BornDate:  a.BirthDate,
		DeathDate: a.DeathDate,
	}

	// Open Library bio can be string or object with "value"
	if a.Bio != nil {
		var bioStr string
		if json.Unmarshal(a.Bio, &bioStr) == nil {
			res.Bio = bioStr
		} else {
			var bioObj struct {
				Value string `json:"value"`
			}
			if json.Unmarshal(a.Bio, &bioObj) == nil {
				res.Bio = bioObj.Value
			}
		}
	}

	if len(a.Photos) > 0 {
		res.ImageURL = fmt.Sprintf("https://covers.openlibrary.org/a/id/%d-M.jpg", a.Photos[0])
	}

	return res, nil
}

func containsFold(s, substr string) bool {
	sLen := len(s)
	subLen := len(substr)
	if subLen == 0 {
		return true
	}
	if subLen > sLen {
		return false
	}
	for i := 0; i <= sLen-subLen; i++ {
		match := true
		for j := 0; j < subLen; j++ {
			if toLower(s[i+j]) != toLower(substr[j]) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func toLower(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 32
	}
	return c
}
