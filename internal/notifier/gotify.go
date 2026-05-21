package notifier

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Gotify struct {
	url   string
	token string
	http  *http.Client
}

func NewGotify(url, token string) *Gotify {
	return &Gotify{
		url:   url,
		token: token,
		http: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

type gotifyMessage struct {
	Title    string `json:"title"`
	Message  string `json:"message"`
	Priority int    `json:"priority"`
}

func (g *Gotify) Send(title, message string, priority int) error {
	body, err := json.Marshal(gotifyMessage{
		Title:    title,
		Message:  message,
		Priority: priority,
	})
	if err != nil {
		return fmt.Errorf("marshaling message: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, g.url+"/message", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gotify-Key", g.token)

	resp, err := g.http.Do(req)
	if err != nil {
		return fmt.Errorf("sending gotify message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("gotify returned %d", resp.StatusCode)
	}
	return nil
}
