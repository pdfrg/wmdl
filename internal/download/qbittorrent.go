package download

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	cookies := q.cookies
	q.mu.Unlock()
	for _, c := range cookies {
		req.AddCookie(c)
	}
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

// addResponse is the JSON response from qBittorrent >= 5.2.0 for torrents/add.
type addResponse struct {
	AddedTorrentIDs []string `json:"added_torrent_ids"`
	FailureCount    int      `json:"failure_count"`
	PendingCount    int      `json:"pending_count"`
	SuccessCount    int      `json:"success_count"`
}

// parseAddResponse extracts the first torrent ID from the add response.
// On qBittorrent >= 5.2.0 the response is JSON; on older versions it's plain text.
func parseAddResponse(body []byte) string {
	var r addResponse
	if err := json.Unmarshal(body, &r); err != nil {
		// old qBittorrent returns "Ok." — no ID available
		return ""
	}
	if len(r.AddedTorrentIDs) > 0 {
		return r.AddedTorrentIDs[0]
	}
	return ""
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
			// qBittorrent >= 5.2.0 returns JSON; older returns "Ok." text
			torrentID := parseAddResponse(body)
			return torrentID, nil
		case http.StatusAccepted, http.StatusNoContent:
			// qBittorrent >= 5.2.0 may return 202 (queued) or 204 (success)
			return "", nil
		case http.StatusConflict:
			// qBittorrent >= 5.2.0 returns 409 for duplicates or invalid input
			return "", fmt.Errorf("qbittorrent add rejected: duplicate or invalid")
		default:
			return "", fmt.Errorf("qbittorrent add returned %d", resp.StatusCode)
		}
	}
	return "", nil
}
