package notifier

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pdfrg/wmdl/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewWebhookGotify(t *testing.T) {
	h, err := NewWebhook(config.NotifierConfig{
		Service: "gotify",
		URL:     "http://gotify:8080",
		Token:   "my-token",
	})
	require.NoError(t, err)
	assert.Equal(t, "http://gotify:8080/message", h.url)
	assert.Equal(t, "my-token", h.headers["X-Gotify-Key"])

	body, err := h.build("title", "message", 5)
	require.NoError(t, err)
	assert.JSONEq(t, `{"title":"title","message":"message","priority":5}`, string(body))
}

func TestNewWebhookGotifyNoToken(t *testing.T) {
	h, err := NewWebhook(config.NotifierConfig{
		Service: "gotify",
		URL:     "http://gotify:8080",
	})
	require.NoError(t, err)
	assert.Equal(t, "http://gotify:8080/message", h.url)
	_, hasKey := h.headers["X-Gotify-Key"]
	assert.False(t, hasKey)
}

func TestNewWebhookSlack(t *testing.T) {
	h, err := NewWebhook(config.NotifierConfig{
		Service: "slack",
		URL:     "https://hooks.slack.com/xyz",
	})
	require.NoError(t, err)
	assert.Equal(t, "https://hooks.slack.com/xyz", h.url)

	body, err := h.build("title", "message", 0)
	require.NoError(t, err)
	assert.JSONEq(t, `{"text":"title\nmessage"}`, string(body))
}

func TestNewWebhookDiscord(t *testing.T) {
	h, err := NewWebhook(config.NotifierConfig{
		Service: "discord",
		URL:     "https://discord.com/api/webhooks/abc",
	})
	require.NoError(t, err)
	assert.Equal(t, "https://discord.com/api/webhooks/abc", h.url)

	body, err := h.build("title", "message", 0)
	require.NoError(t, err)
	assert.JSONEq(t, `{"content":"**title**\nmessage"}`, string(body))
}

func TestNewWebhookNtfy(t *testing.T) {
	h, err := NewWebhook(config.NotifierConfig{
		Service: "ntfy",
		URL:     "https://ntfy.sh/mytopic",
		Token:   "tk_abc123",
	})
	require.NoError(t, err)
	assert.Equal(t, "https://ntfy.sh/mytopic", h.url)
	assert.Equal(t, "Bearer tk_abc123", h.headers["Authorization"])

	body, err := h.build("title", "message", 4)
	require.NoError(t, err)
	assert.JSONEq(t, `{"topic":"","title":"title","message":"message","priority":4}`, string(body))
}

func TestNewWebhookNtfyNoToken(t *testing.T) {
	h, err := NewWebhook(config.NotifierConfig{
		Service: "ntfy",
		URL:     "https://ntfy.sh/mytopic",
	})
	require.NoError(t, err)
	_, hasAuth := h.headers["Authorization"]
	assert.False(t, hasAuth)
}

func TestNewWebhookDefault(t *testing.T) {
	t.Run("unknown_service", func(t *testing.T) {
		h, err := NewWebhook(config.NotifierConfig{
			Service: "webhook",
			URL:     "http://example.com/hook",
			Token:   "secret",
		})
		require.NoError(t, err)
		assert.Equal(t, "http://example.com/hook", h.url)
		assert.Equal(t, "application/json", h.headers["Content-Type"])

		body, err := h.build("t", "m", 1)
		require.NoError(t, err)
		assert.JSONEq(t, `{}`, string(body))
	})

	t.Run("no_service_no_template", func(t *testing.T) {
		h, err := NewWebhook(config.NotifierConfig{
			URL: "http://example.com/hook",
		})
		require.NoError(t, err)
		assert.Equal(t, "http://example.com/hook", h.url)

		body, err := h.build("t", "m", 1)
		require.NoError(t, err)
		assert.JSONEq(t, `{}`, string(body))
	})
}

func TestNewWebhookCustomTemplate(t *testing.T) {
	t.Run("valid_template", func(t *testing.T) {
		h, err := NewWebhook(config.NotifierConfig{
			Service:        "custom",
			URL:            "http://example.com/hook",
			CustomTemplate: `{"title":"{{.Title}}","msg":"{{.Message}}","prio":{{.Priority}}}`,
		})
		require.NoError(t, err)

		body, err := h.build("hello", "world", 3)
		require.NoError(t, err)
		assert.JSONEq(t, `{"title":"hello","msg":"world","prio":3}`, string(body))
	})

	t.Run("with_json_escape", func(t *testing.T) {
		h, err := NewWebhook(config.NotifierConfig{
			Service:        "custom",
			URL:            "http://example.com/hook",
			CustomTemplate: `{"msg":"{{jsonEscape .Message}}"}`,
		})
		require.NoError(t, err)

		body, err := h.build("t", `he said "hello"`, 0)
		require.NoError(t, err)
		assert.JSONEq(t, `{"msg":"he said \"hello\""}`, string(body))
	})

	t.Run("invalid_template", func(t *testing.T) {
		_, err := NewWebhook(config.NotifierConfig{
			CustomTemplate: `{{.BadSyntax`,
		})
		assert.ErrorContains(t, err, "parsing notifier template")
	})
}

func TestWebhookSend(t *testing.T) {
	t.Run("gotify_success", func(t *testing.T) {
		var gotBody []byte
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotBody, _ = io.ReadAll(r.Body)
			assert.Equal(t, "my-token", r.Header.Get("X-Gotify-Key"))
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		w, err := NewWebhook(config.NotifierConfig{
			Service: "gotify",
			URL:     srv.URL,
			Token:   "my-token",
		})
		require.NoError(t, err)
		require.NoError(t, w.Send("title", "msg", 3))
		assert.JSONEq(t, `{"title":"title","message":"msg","priority":3}`, string(gotBody))
	})

	t.Run("slack_success", func(t *testing.T) {
		var gotBody []byte
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotBody, _ = io.ReadAll(r.Body)
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		w, err := NewWebhook(config.NotifierConfig{
			Service: "slack",
			URL:     srv.URL,
		})
		require.NoError(t, err)
		require.NoError(t, w.Send("Alert", "something happened", 0))
		assert.JSONEq(t, `{"text":"Alert\nsomething happened"}`, string(gotBody))
	})

	t.Run("discord_success", func(t *testing.T) {
		var gotBody []byte
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		w, err := NewWebhook(config.NotifierConfig{
			Service: "discord",
			URL:     srv.URL,
		})
		require.NoError(t, err)
		require.NoError(t, w.Send("Header", "body text", 0))
		assert.JSONEq(t, `{"content":"**Header**\nbody text"}`, string(gotBody))
	})

	t.Run("ntfy_success", func(t *testing.T) {
		var gotBody []byte
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotBody, _ = io.ReadAll(r.Body)
			assert.Equal(t, "Bearer tk_xyz", r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		w, err := NewWebhook(config.NotifierConfig{
			Service: "ntfy",
			URL:     srv.URL,
			Token:   "tk_xyz",
		})
		require.NoError(t, err)
		require.NoError(t, w.Send("n", "m", 2))
		assert.JSONEq(t, `{"topic":"","title":"n","message":"m","priority":2}`, string(gotBody))
	})

	t.Run("non_2xx_status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		defer srv.Close()

		w, err := NewWebhook(config.NotifierConfig{
			Service: "gotify",
			URL:     srv.URL,
		})
		require.NoError(t, err)
		err = w.Send("t", "m", 0)
		assert.ErrorContains(t, err, "403")
	})

	t.Run("default_service_send", func(t *testing.T) {
		var gotBody []byte
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotBody, _ = io.ReadAll(r.Body)
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		w, err := NewWebhook(config.NotifierConfig{
			URL: srv.URL,
		})
		require.NoError(t, err)
		require.NoError(t, w.Send("t", "m", 0))
		assert.JSONEq(t, `{}`, string(gotBody))
	})
}

func TestJsonEscape(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{`he said "hello"`, `he said \"hello\"`},
		{"a\nb", "a\\nb"},
		{"back\\slash", "back\\\\slash"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.want, jsonEscape(tt.input))
		})
	}
}

func TestNew(t *testing.T) {
	t.Run("returns_webhook", func(t *testing.T) {
		n, err := New(config.NotifierConfig{
			Service: "gotify",
			URL:     "http://gotify:8080",
			Token:   "tk",
		})
		require.NoError(t, err)
		_, ok := n.(*webhook)
		assert.True(t, ok)
	})

	t.Run("passes_config_to_webhook", func(t *testing.T) {
		n, err := New(config.NotifierConfig{
			Service: "slack",
			URL:     "https://hooks.slack.com/xyz",
		})
		require.NoError(t, err)
		w := n.(*webhook)
		assert.Equal(t, "https://hooks.slack.com/xyz", w.url)
	})
}
