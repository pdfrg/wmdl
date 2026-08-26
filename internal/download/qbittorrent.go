package download

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type QbittorrentClient struct {
	baseURL  string
	username string
	password string
	http     *http.Client
	mu       sync.Mutex
	cookies  []*http.Cookie
	loggedIn bool
}

var _ Client = (*QbittorrentClient)(nil)

func NewQbittorrentClient(baseURL, username, password string) *QbittorrentClient {
	return &QbittorrentClient{
		baseURL:  strings.TrimRight(baseURL, "/"),
		username: username,
		password: password,
		http: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 3 {
					return fmt.Errorf("too many redirects")
				}
				return nil
			},
		},
	}
}

func (q *QbittorrentClient) login(ctx context.Context) error {
	q.mu.Lock()
	alreadyLoggedIn := q.loggedIn && len(q.cookies) > 0
	q.mu.Unlock()
	if alreadyLoggedIn {
		return nil
	}

	v := url.Values{}
	v.Set("username", q.username)
	v.Set("password", q.password)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, q.baseURL+"/api/v2/auth/login", strings.NewReader(v.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := q.http.Do(req)
	if err != nil {
		return fmt.Errorf("qbittorrent login: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("qbittorrent login: reading response: %w", err)
		}
		if strings.Contains(string(body), "Fails") {
			return fmt.Errorf("qbittorrent login failed: invalid credentials")
		}
	case http.StatusNoContent:
		// qBittorrent >= 5.2.0 returns 204 on success
	default:
		return fmt.Errorf("qbittorrent login returned %d", resp.StatusCode)
	}

	q.mu.Lock()
	q.cookies = resp.Cookies()
	q.loggedIn = true
	q.mu.Unlock()
	return nil
}

func (q *QbittorrentClient) setAuth(req *http.Request) {
	q.mu.Lock()
	for _, c := range q.cookies {
		req.AddCookie(c)
	}
	q.mu.Unlock()
}

func (q *QbittorrentClient) Ping(ctx context.Context) error {
	return q.login(ctx)
}

func (q *QbittorrentClient) AddTorrent(ctx context.Context, torrentURL string, opts ...Option) (string, error) {
	return q.add(ctx, "urls", torrentURL, opts...)
}

func (q *QbittorrentClient) AddMagnet(ctx context.Context, magnetURI string, opts ...Option) (string, error) {
	return q.add(ctx, "urls", magnetURI, opts...)
}

// AddTorrentData uploads the raw .torrent file bytes via the `torrents`
// multipart field. This is the reliable path: once the bytes are here the add
// is synchronous and the response returns the added torrent hash directly.
func (q *QbittorrentClient) AddTorrentData(ctx context.Context, name string, data []byte, opts ...Option) (string, error) {
	opt := &AddOptions{}
	for _, o := range opts {
		o(opt)
	}

	for attempt := 0; attempt < 2; attempt++ {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		fw, err := w.CreateFormFile("torrents", name)
		if err != nil {
			return "", err
		}
		if _, err := fw.Write(data); err != nil {
			return "", err
		}
		if opt.SavePath != "" {
			_ = w.WriteField("savepath", opt.SavePath)
		}
		if opt.Category != "" {
			_ = w.WriteField("category", opt.Category)
		}
		if opt.Paused {
			_ = w.WriteField("paused", "true")
		}
		if err := w.Close(); err != nil {
			return "", err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, q.baseURL+"/api/v2/torrents/add", &buf)
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", w.FormDataContentType())
		if err := q.login(ctx); err != nil {
			return "", err
		}
		q.setAuth(req)

		resp, err := q.http.Do(req)
		if err != nil {
			return "", fmt.Errorf("qbittorrent add: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusForbidden && attempt == 0 {
			q.mu.Lock()
			q.loggedIn = false
			q.mu.Unlock()
			continue
		}

		switch resp.StatusCode {
		case http.StatusOK:
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return "", fmt.Errorf("qbittorrent add: reading response: %w", err)
			}
			torrentID, err := parseAddResponse(body)
			return torrentID, err
		case http.StatusAccepted, http.StatusNoContent:
			return "", nil
		case http.StatusConflict:
			return "", fmt.Errorf("qbittorrent add rejected: duplicate or invalid")
		default:
			return "", fmt.Errorf("qbittorrent add returned %d", resp.StatusCode)
		}
	}
	return "", nil
}

// addResponse is the JSON response from qBittorrent >= 5.2.0 for torrents/add.
type addResponse struct {
	AddedTorrentIDs []string `json:"added_torrent_ids"`
	FailureCount    int      `json:"failure_count"`
	PendingCount    int      `json:"pending_count"`
	SuccessCount    int      `json:"success_count"`
}

// parseAddResponse extracts the first added torrent ID. On qBittorrent >= 5.2.0
// the response is JSON; on older versions it's plain text ("Ok.") and no ID is
// available. When the JSON indicates that no torrent was actually added (e.g.
// the URL fetch failed or is still pending), a non-nil error is returned so the
// caller does not silently treat the add as successful.
func parseAddResponse(body []byte) (string, error) {
	var r addResponse
	if err := json.Unmarshal(body, &r); err != nil {
		// old qBittorrent returns "Ok." — no ID available, assume accepted.
		return "", nil
	}
	if len(r.AddedTorrentIDs) > 0 {
		return r.AddedTorrentIDs[0], nil
	}
	if r.FailureCount > 0 || r.PendingCount > 0 || r.SuccessCount > 0 {
		return "", fmt.Errorf("qbittorrent add: no torrent added (failed=%d, pending=%d)", r.FailureCount, r.PendingCount)
	}
	return "", nil
}

func (q *QbittorrentClient) add(ctx context.Context, field, value string, opts ...Option) (string, error) {
	if err := q.login(ctx); err != nil {
		return "", err
	}

	opt := &AddOptions{}
	for _, o := range opts {
		o(opt)
	}

	for attempt := 0; attempt < 2; attempt++ {
		v := url.Values{}
		v.Set(field, value)
		if opt.SavePath != "" {
			v.Set("savepath", opt.SavePath)
		}
		if opt.Category != "" {
			v.Set("category", opt.Category)
		}
		if opt.Paused {
			v.Set("paused", "true")
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, q.baseURL+"/api/v2/torrents/add", strings.NewReader(v.Encode()))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		q.setAuth(req)

		resp, err := q.http.Do(req)
		if err != nil {
			return "", fmt.Errorf("qbittorrent add: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusForbidden && attempt == 0 {
			q.mu.Lock()
			q.loggedIn = false
			q.mu.Unlock()
			if err := q.login(ctx); err != nil {
				return "", err
			}
			continue
		}

		switch resp.StatusCode {
		case http.StatusOK:
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return "", fmt.Errorf("qbittorrent add: reading response: %w", err)
			}
			torrentID, err := parseAddResponse(body)
			return torrentID, err
		case http.StatusAccepted, http.StatusNoContent:
			return "", nil
		case http.StatusConflict:
			return "", fmt.Errorf("qbittorrent add rejected: duplicate or invalid")
		default:
			return "", fmt.Errorf("qbittorrent add returned %d", resp.StatusCode)
		}
	}
	return "", nil
}
