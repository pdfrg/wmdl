package download

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

type DelugeClient struct {
	baseURL  string
	password string
	http     *http.Client
}

var _ Client = (*DelugeClient)(nil)

func NewDelugeClient(baseURL, password string) *DelugeClient {
	return &DelugeClient{
		baseURL:  strings.TrimRight(baseURL, "/"),
		password: password,
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

type delugeRequest struct {
	Method string        `json:"method"`
	Params []interface{} `json:"params"`
	ID     int           `json:"id"`
}

type delugeResponse struct {
	Result interface{}  `json:"result"`
	Error  *delugeError `json:"error,omitempty"`
	ID     int          `json:"id"`
}

type delugeError struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

var (
	delugeReqID int
	delugeReqMu sync.Mutex
)

func (d *DelugeClient) call(ctx context.Context, method string, params []interface{}) (*delugeResponse, error) {
	delugeReqMu.Lock()
	delugeReqID++
	id := delugeReqID
	delugeReqMu.Unlock()
	body, err := json.Marshal(delugeRequest{
		Method: method,
		Params: params,
		ID:     id,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.baseURL+"/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("deluge call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("deluge returned %d", resp.StatusCode)
	}

	var dr delugeResponse
	if err := json.NewDecoder(resp.Body).Decode(&dr); err != nil {
		return nil, err
	}
	if dr.Error != nil {
		return nil, fmt.Errorf("deluge error: %s (code %d)", dr.Error.Message, dr.Error.Code)
	}
	return &dr, nil
}

func (d *DelugeClient) login(ctx context.Context) error {
	resp, err := d.call(ctx, "auth.login", []interface{}{d.password})
	if err != nil {
		return err
	}
	ok, okCheck := resp.Result.(bool)
	if !okCheck {
		return fmt.Errorf("deluge login: unexpected response type %T", resp.Result)
	}
	if !ok {
		return fmt.Errorf("deluge login failed")
	}
	return nil
}

func (d *DelugeClient) Ping(ctx context.Context) error {
	return d.login(ctx)
}

func (d *DelugeClient) AddTorrent(ctx context.Context, torrentURL string, opts ...Option) (string, error) {
	return d.add(ctx, torrentURL, opts...)
}

func (d *DelugeClient) AddMagnet(ctx context.Context, magnetURI string, opts ...Option) (string, error) {
	return d.add(ctx, magnetURI, opts...)
}

func (d *DelugeClient) add(ctx context.Context, uri string, opts ...Option) (string, error) {
	if err := d.login(ctx); err != nil {
		return "", err
	}

	opt := &AddOptions{}
	for _, o := range opts {
		o(opt)
	}

	delugeOpts := map[string]interface{}{}
	if opt.SavePath != "" {
		delugeOpts["download_location"] = opt.SavePath
	}
	if opt.Paused {
		delugeOpts["add_paused"] = true
	}
	if opt.Category != "" {
		delugeOpts["label"] = opt.Category
	}

	resp, err := d.call(ctx, "core.add_torrent_url", []interface{}{uri, delugeOpts})
	if err != nil {
		return "", err
	}

	torrentID, ok := resp.Result.(string)
	if !ok {
		return "", fmt.Errorf("deluge add torrent: unexpected response type %T", resp.Result)
	}
	return torrentID, nil
}
