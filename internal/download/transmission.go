package download

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

type TransmissionClient struct {
	baseURL  string
	username string
	password string
	http     *http.Client

	mu        sync.Mutex
	sessionID string
}

var _ Client = (*TransmissionClient)(nil)

func NewTransmissionClient(baseURL, username, password string) *TransmissionClient {
	return &TransmissionClient{
		baseURL:  strings.TrimRight(baseURL, "/"),
		username: username,
		password: password,
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

type trpcRequest struct {
	Method    string      `json:"method"`
	Arguments interface{} `json:"arguments,omitempty"`
	Tag       int         `json:"tag,omitempty"`
}

type trpcResponse struct {
	Result    string          `json:"result"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Tag       int             `json:"tag,omitempty"`
}

func (t *TransmissionClient) getSessionID(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/transmission/rpc", nil)
	if err != nil {
		return err
	}
	t.setAuth(req)

	resp, err := t.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusConflict {
		sid := resp.Header.Get("X-Transmission-Session-Id")
		if sid == "" {
			return fmt.Errorf("transmission: no session ID in 409 response")
		}
		t.mu.Lock()
		t.sessionID = sid
		t.mu.Unlock()
		return nil
	}

	return fmt.Errorf("transmission: unexpected status %d getting session", resp.StatusCode)
}

func (t *TransmissionClient) do(ctx context.Context, method string, args interface{}) (*trpcResponse, error) {
	return t.doWithRetry(ctx, method, args, 0)
}

func (t *TransmissionClient) doWithRetry(ctx context.Context, method string, args interface{}, retry int) (*trpcResponse, error) {
	t.mu.Lock()
	sid := t.sessionID
	t.mu.Unlock()
	if sid == "" {
		if err := t.getSessionID(ctx); err != nil {
			return nil, err
		}
		t.mu.Lock()
		sid = t.sessionID
		t.mu.Unlock()
	}

	body, err := json.Marshal(trpcRequest{
		Method:    method,
		Arguments: args,
		Tag:       time.Now().Nanosecond(),
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/transmission/rpc", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Transmission-Session-Id", sid)
	t.setAuth(req)

	resp, err := t.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusConflict {
		if retry >= 1 {
			return nil, fmt.Errorf("transmission: still getting 409 after retry")
		}
		sid := resp.Header.Get("X-Transmission-Session-Id")
		t.mu.Lock()
		t.sessionID = sid
		t.mu.Unlock()
		return t.doWithRetry(ctx, method, args, retry+1)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("transmission returned %d", resp.StatusCode)
	}

	var trpc trpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&trpc); err != nil {
		return nil, err
	}
	return &trpc, nil
}

func (t *TransmissionClient) setAuth(req *http.Request) {
	if t.username != "" {
		req.SetBasicAuth(t.username, t.password)
	}
}

type torrentAddArgs struct {
	Filename    string   `json:"filename,omitempty"`
	Metainfo    string   `json:"metainfo,omitempty"`
	DownloadDir string   `json:"download-dir,omitempty"`
	Paused      bool     `json:"paused,omitempty"`
	Labels      []string `json:"labels,omitempty"`
}

func (t *TransmissionClient) Ping(ctx context.Context) error {
	return t.getSessionID(ctx)
}

func (t *TransmissionClient) AddTorrent(ctx context.Context, torrentURL string, opts ...Option) (string, error) {
	return t.addTorrent(ctx, torrentURL, false, opts...)
}

func (t *TransmissionClient) AddMagnet(ctx context.Context, magnetURI string, opts ...Option) (string, error) {
	return t.addTorrent(ctx, magnetURI, false, opts...)
}

// AddTorrentData adds a torrent from its raw .torrent bytes (base64 metainfo).
func (t *TransmissionClient) AddTorrentData(ctx context.Context, name string, data []byte, opts ...Option) (string, error) {
	opt := &AddOptions{}
	for _, o := range opts {
		o(opt)
	}

	args := torrentAddArgs{
		Metainfo: base64.StdEncoding.EncodeToString(data),
		Paused:   opt.Paused,
		Labels:   nil,
	}
	if opt.Category != "" {
		args.Labels = []string{opt.Category}
	}
	if opt.SavePath != "" {
		args.DownloadDir = opt.SavePath
	}

	resp, err := t.do(ctx, "torrent-add", args)
	if err != nil {
		return "", err
	}
	if resp.Result != "success" {
		return "", fmt.Errorf("transmission torrent-add failed: %s", resp.Result)
	}

	var added struct {
		TorrentAdded *struct {
			HashString string `json:"hashString"`
		} `json:"torrent-added"`
	}
	if len(resp.Arguments) > 0 {
		_ = json.Unmarshal(resp.Arguments, &added)
	}
	if added.TorrentAdded != nil {
		return added.TorrentAdded.HashString, nil
	}
	return "", nil
}

func (t *TransmissionClient) addTorrent(ctx context.Context, value string, isMagnet bool, opts ...Option) (string, error) {
	opt := &AddOptions{}
	for _, o := range opts {
		o(opt)
	}

	args := torrentAddArgs{
		Filename: value,
		Paused:   opt.Paused,
		Labels:   nil,
	}
	if opt.Category != "" {
		args.Labels = []string{opt.Category}
	}
	if opt.SavePath != "" {
		args.DownloadDir = opt.SavePath
	}

	resp, err := t.do(ctx, "torrent-add", args)
	if err != nil {
		return "", err
	}
	if resp.Result != "success" {
		return "", fmt.Errorf("transmission torrent-add failed: %s", resp.Result)
	}

	return "", nil
}
