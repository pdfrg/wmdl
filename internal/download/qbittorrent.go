package download

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type QbittorrentClient struct {
	baseURL  string
	username string
	password string
	http     *http.Client
	cookies  []*http.Cookie
}

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

func (q *QbittorrentClient) login() error {
	v := url.Values{}
	v.Set("username", q.username)
	v.Set("password", q.password)

	req, err := http.NewRequest(http.MethodPost, q.baseURL+"/api/v2/auth/login", strings.NewReader(v.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := q.http.Do(req)
	if err != nil {
		return fmt.Errorf("qbittorrent login: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("qbittorrent login returned %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "Fails") {
		return fmt.Errorf("qbittorrent login failed: invalid credentials")
	}

	q.cookies = resp.Cookies()
	return nil
}

func (q *QbittorrentClient) setAuth(req *http.Request) {
	for _, c := range q.cookies {
		req.AddCookie(c)
	}
}

func (q *QbittorrentClient) AddTorrent(torrentURL string, opts ...Option) (string, error) {
	return q.add("urls", torrentURL, opts...)
}

func (q *QbittorrentClient) AddMagnet(magnetURI string, opts ...Option) (string, error) {
	return q.add("urls", magnetURI, opts...)
}

func (q *QbittorrentClient) add(field, value string, opts ...Option) (string, error) {
	if err := q.login(); err != nil {
		return "", err
	}

	opt := &AddOptions{}
	for _, o := range opts {
		o(opt)
	}

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

	req, err := http.NewRequest(http.MethodPost, q.baseURL+"/api/v2/torrents/add", strings.NewReader(v.Encode()))
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

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("qbittorrent add returned %d", resp.StatusCode)
	}

	return "", nil
}
