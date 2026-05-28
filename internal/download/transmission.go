package download

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type TransmissionClient struct {
	baseURL   string
	username  string
	password  string
	sessionID string
	http      *http.Client
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
		t.sessionID = resp.Header.Get("X-Transmission-Session-Id")
		if t.sessionID == "" {
			return fmt.Errorf("transmission: no session ID in 409 response")
		}
		return nil
	}

	return fmt.Errorf("transmission: unexpected status %d getting session", resp.StatusCode)
}

func (t *TransmissionClient) do(ctx context.Context, method string, args interface{}) (*trpcResponse, error) {
	if t.sessionID == "" {
		if err := t.getSessionID(ctx); err != nil {
			return nil, err
		}
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
	req.Header.Set("X-Transmission-Session-Id", t.sessionID)
	t.setAuth(req)

	resp, err := t.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusConflict {
		t.sessionID = resp.Header.Get("X-Transmission-Session-Id")
		return t.do(ctx, method, args)
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
