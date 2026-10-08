package auth

import (
	"errors"
	"net/http"
	"strings"
)

var ErrARLMissing = errors.New("arl is required")

func NormalizeARL(input string) (string, error) {
	value := strings.TrimSpace(input)
	if value == "" {
		return "", ErrARLMissing
	}

	if strings.Contains(value, "arl=") {
		header := value
		if i := strings.Index(header, ":"); i >= 0 && strings.Contains(strings.ToLower(header[:i]), "cookie") {
			header = strings.TrimSpace(header[i+1:])
		}
		req := http.Request{Header: http.Header{"Cookie": []string{header}}}
		if cookie, err := req.Cookie("arl"); err == nil {
			value = cookie.Value
		}
	}

	value = strings.TrimSpace(value)
	if value == "" {
		return "", ErrARLMissing
	}
	return value, nil
}

func MaskARL(arl string) string {
	value := strings.TrimSpace(arl)
	if value == "" {
		return ""
	}
	if len(value) <= 8 {
		return "********"
	}
	return value[:4] + strings.Repeat("*", len(value)-8) + value[len(value)-4:]
}
