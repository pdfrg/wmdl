package discover

import (
	"encoding/json"
	"fmt"
	"strings"
)

// maxAPIFailureDetail caps the human-readable detail extracted from an API
// error body before it reaches the notification. Long enough for a real
// sentence, short enough to stay on one notification line.
const maxAPIFailureDetail = 200

// sanitizeFailureReason collapses all whitespace (including newlines from
// pretty-printed JSON or wrapped chromedp errors) to single spaces and
// truncates to maxLen runes, so failure reasons always render as a single
// short notification line. It is the central guard in markScrapeFailures and
// covers every provider, including ones that return ad-hoc error strings.
func sanitizeFailureReason(s string, maxLen int) string {
	s = strings.Join(strings.Fields(s), " ")
	if maxLen <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	return string(r[:maxLen-1]) + "…"
}

// extractAPIMessage pulls the human-readable message out of a JSON error
// body. It understands GraphQL {"errors":[{"message":...}]} (AniList) and
// generic {"message":...} / {"error":"..."} shapes (Jikan, Tenrai). Returns
// "" when the body is not JSON or carries no message.
func extractAPIMessage(body []byte) string {
	var gql struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &gql); err == nil {
		for _, e := range gql.Errors {
			if strings.TrimSpace(e.Message) != "" {
				return e.Message
			}
		}
	}

	var generic struct {
		Message string `json:"message"`
		Error   any    `json:"error"`
	}
	if err := json.Unmarshal(body, &generic); err == nil {
		if strings.TrimSpace(generic.Message) != "" {
			return generic.Message
		}
		if s, ok := generic.Error.(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// apiErrorDetail returns the notification-safe detail for a non-2xx API
// response body: the extracted message when the body carries one, otherwise
// a whitespace-collapsed, truncated snippet of the raw body.
func apiErrorDetail(body []byte, maxLen int) string {
	if msg := extractAPIMessage(body); msg != "" {
		return sanitizeFailureReason(msg, maxLen)
	}
	return sanitizeFailureReason(string(body), maxLen)
}

// newAPIStatusError builds a single-line "provider returned CODE: detail"
// error from a non-2xx response body.
func newAPIStatusError(provider string, code int, body []byte) error {
	return fmt.Errorf("%s returned %d: %s", provider, code, apiErrorDetail(body, maxAPIFailureDetail))
}
