package shortener

import (
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Field + ": " + e.Message
}

func ValidateURL(raw string) error {
	invalid := &ValidationError{Field: "url", Message: "URL must be an absolute HTTP or HTTPS address without credentials, spaces, control characters or backslashes"}
	if len(raw) == 0 || len(raw) > 2048 || !utf8.ValidString(raw) {
		return invalid
	}
	for _, char := range raw {
		if unicode.IsSpace(char) || unicode.IsControl(char) || char == '\\' {
			return invalid
		}
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Hostname() == "" || parsed.Opaque != "" || parsed.User != nil {
		return invalid
	}
	if strings.HasPrefix(parsed.Host, "[") {
		if _, err := netip.ParseAddr(parsed.Hostname()); err != nil {
			return invalid
		}
	}
	if strings.HasSuffix(parsed.Host, ":") {
		return invalid
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return invalid
		}
	}
	return nil
}

func ValidateAlias(alias string) error {
	invalid := &ValidationError{Field: "alias", Message: "Alias must contain 3 to 32 characters from A-Z, a-z, 0-9, underscore and hyphen"}
	if len(alias) < 3 || len(alias) > 32 {
		return invalid
	}
	for _, char := range alias {
		if !(char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' ||
			char >= '0' && char <= '9' || char == '_' || char == '-') {
			return invalid
		}
	}
	return nil
}
