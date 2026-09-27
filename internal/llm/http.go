package llm

import (
	"fmt"
	"time"
)

func classifyHTTP(provider string, status int, body []byte, now func() time.Time) error {
	_ = body // intentionally unused — never log response bodies that may contain secrets
	switch {
	case status == 429:
		return &ErrUnavailable{
			Provider: provider,
			Reason:   "429",
			Until:    now().Add(defaultBackoff("429")),
		}
	case status >= 500:
		return &ErrUnavailable{
			Provider: provider,
			Reason:   "5xx",
			Until:    now().Add(defaultBackoff("5xx")),
			Err:      fmt.Errorf("http %d", status),
		}
	case status < 200 || status >= 300:
		return fmt.Errorf("%s: http %d", provider, status)
	default:
		return nil
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
