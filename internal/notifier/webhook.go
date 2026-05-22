package notifier

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"text/template"
	"time"

	"github.com/pdfrg/wmdl/internal/config"
)

type webhook struct {
	url      string
	headers  map[string]string
	bodyTmpl *template.Template
	client   *http.Client
}

type tmplData struct {
	Title    string
	Message  string
	Priority int
}

func NewWebhook(cfg config.NotifierConfig) *webhook {
	h := &webhook{
		client: &http.Client{Timeout: 10 * time.Second},
	}

	svc := strings.ToLower(cfg.Service)
	url := cfg.URL
	token := cfg.Token
	tmplStr := cfg.CustomTemplate

	switch svc {
	case "gotify":
		h.url = strings.TrimRight(url, "/") + "/message"
		if token != "" {
			h.headers = map[string]string{"X-Gotify-Key": token}
		}
		if tmplStr == "" {
			tmplStr = `{"title":"{{.Title}}","message":"{{.Message}}","priority":{{.Priority}}}`
		}

	case "slack":
		h.url = url
		if tmplStr == "" {
			tmplStr = `{"text":"{{.Title}}\n{{.Message}}"}`
		}

	case "discord":
		h.url = url
		if tmplStr == "" {
			tmplStr = `{"content":"**{{.Title}}**\n{{.Message}}"}`
		}

	case "ntfy":
		h.url = url
		if token != "" {
			h.headers = map[string]string{"Authorization": "Bearer " + token}
		}
		if tmplStr == "" {
			tmplStr = `{"topic":"","title":"{{.Title}}","message":"{{.Message}}","priority":{{.Priority}}}`
		}

	default:
		// "webhook" or unknown — require custom_template
		h.url = url
		if token != "" {
			h.headers = map[string]string{
				"Content-Type": "application/json",
			}
		}
		if tmplStr == "" {
			tmplStr = `{}`
		}
	}

	if h.headers == nil {
		h.headers = map[string]string{}
	}
	if _, hasCT := h.headers["Content-Type"]; !hasCT {
		h.headers["Content-Type"] = "application/json"
	}

	h.bodyTmpl = template.Must(template.New("body").Parse(tmplStr))
	return h
}

func (w *webhook) Send(title, message string, priority int) error {
	var buf bytes.Buffer
	if err := w.bodyTmpl.Execute(&buf, tmplData{
		Title:    title,
		Message:  message,
		Priority: priority,
	}); err != nil {
		return fmt.Errorf("rendering body: %w", err)
	}

	// Validate it's valid JSON
	if !json.Valid(buf.Bytes()) {
		return fmt.Errorf("rendered body is not valid JSON: %s", buf.String())
	}

	req, err := http.NewRequest(http.MethodPost, w.url, &buf)
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
