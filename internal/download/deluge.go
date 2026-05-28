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

var delugeReqID int

func (d *DelugeClient) call(method string, params []interface{}) (*delugeResponse, error) {
	delugeReqID++
	body, err := json.Marshal(delugeRequest{
		Method: method,
		Params: params,
		ID:     delugeReqID,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, d.baseURL+"/json", bytes.NewReader(body))
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

func (d *DelugeClient) login() error {
	resp, err := d.call("auth.login", []interface{}{d.password})
	if err != nil {
		return err
	}
	ok, _ := resp.Result.(bool)
	if !ok {
		return fmt.Errorf("deluge login failed")
	}
	return nil
}

func (d *DelugeClient) Ping(ctx context.Context) error {
	return d.login()
}

func (d *DelugeClient) AddTorrent(ctx context.Context, torrentURL string, opts ...Option) (string, error) {
	return d.add(ctx, torrentURL, opts...)
}

func (d *DelugeClient) AddMagnet(ctx context.Context, magnetURI string, opts ...Option) (string, error) {
	return d.add(ctx, magnetURI, opts...)
}

func (d *DelugeClient) add(ctx context.Context, uri string, opts ...Option) (string, error) {
	if err := d.login(); err != nil {
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

	resp, err := d.call("core.add_torrent_url", []interface{}{uri, delugeOpts})
	if err != nil {
		return "", err
	}

	torrentID, _ := resp.Result.(string)
	return torrentID, nil
}
