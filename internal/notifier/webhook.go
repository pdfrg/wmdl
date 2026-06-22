package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"text/template"
	"time"

	"github.com/pdfrg/wmdl/internal/config"
)

type bodyBuilder func(title, message string, priority int) ([]byte, error)

type webhook struct {
	url     string
	headers map[string]string
	client  *http.Client
	build   bodyBuilder
}

type tmplData struct {
	Title    string
	Message  string
	Priority int
}

var _ Notifier = (*webhook)(nil)

func NewWebhook(cfg config.NotifierConfig) (*webhook, error) {
	h := &webhook{
		client: &http.Client{Timeout: 10 * time.Second},
	}

	svc := strings.ToLower(cfg.Service)
	url := cfg.URL
	token := cfg.Token

	switch svc {
	case "gotify":
		h.url = strings.TrimRight(url, "/") + "/message"
		if token != "" {
			h.headers = map[string]string{"X-Gotify-Key": token}
		}
		h.build = func(title, message string, priority int) ([]byte, error) {
			return json.Marshal(map[string]interface{}{
				"title":    title,
				"message":  message,
				"priority": priority,
			})
		}

	case "slack":
		h.url = url
		h.build = func(title, message string, priority int) ([]byte, error) {
			return json.Marshal(map[string]interface{}{
				"text": title + "\n" + message,
			})
		}

	case "discord":
		h.url = url
		h.build = func(title, message string, priority int) ([]byte, error) {
			return json.Marshal(map[string]interface{}{
				"content": "**" + title + "**\n" + message,
			})
		}

	case "ntfy":
		h.url = url
		if token != "" {
			h.headers = map[string]string{"Authorization": "Bearer " + token}
		}
		h.build = func(title, message string, priority int) ([]byte, error) {
			return json.Marshal(map[string]interface{}{
				"topic":    "",
				"title":    title,
				"message":  message,
				"priority": priority,
			})
		}

	default:
		// "webhook" or unknown — require custom_template
		h.url = url
		if token != "" {
			h.headers = map[string]string{
				"Content-Type": "application/json",
			}
		}
		if cfg.CustomTemplate != "" {
			tmpl, err := template.New("body").Funcs(template.FuncMap{
				"jsonEscape": jsonEscape,
			}).Parse(cfg.CustomTemplate)
			if err != nil {
				return nil, fmt.Errorf("parsing notifier template: %w", err)
			}
			h.build = func(title, message string, priority int) ([]byte, error) {
				var buf bytes.Buffer
				if err := tmpl.Execute(&buf, tmplData{
					Title:    title,
					Message:  message,
					Priority: priority,
				}); err != nil {
					return nil, fmt.Errorf("rendering body: %w", err)
				}
				return buf.Bytes(), nil
			}
		} else {
			h.build = func(title, message string, priority int) ([]byte, error) {
				return []byte("{}"), nil
			}
		}
	}

	if h.headers == nil {
		h.headers = map[string]string{}
	}
	if _, hasCT := h.headers["Content-Type"]; !hasCT {
		h.headers["Content-Type"] = "application/json"
	}

	return h, nil
}

func (w *webhook) Send(title, message string, priority int) error {
	body, err := w.build(title, message, priority)
	if err != nil {
		return fmt.Errorf("building body: %w", err)
	}

	if !json.Valid(body) {
		return fmt.Errorf("rendered body is not valid JSON: %s", string(body))
	}

	sendCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(sendCtx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	for k, v := range w.headers {
		req.Header.Set(k, v)
	}

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("sending: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("webhook returned %d", resp.StatusCode)
	}
	return nil
}

func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	// Remove surrounding quotes for use inside quoted JSON values
	return string(b[1 : len(b)-1])
}
