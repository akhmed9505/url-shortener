package shortener

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateURL(t *testing.T) {
	for _, raw := range []string{
		"http://example.org", "https://example.org/path?q=a%20b#fragment",
		"https://localhost:8443/", "http://127.0.0.1", "http://[::1]:8080/",
		"HTTPS://example.org", "https://example.org/путь",
		"https://example.org/" + strings.Repeat("a", 2028),
	} {
		t.Run(raw, func(t *testing.T) {
			if err := ValidateURL(raw); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, raw := range []string{
		"", "https://example.org/" + strings.Repeat("a", 2029),
		"https://example.org/" + strings.Repeat("я", 1015),
		"/relative", "//example.org", "ftp://example.org",
		"javascript:alert(1)", "https:example.org", "https:///path", "https://:443/path",
		"https://user:secret@example.org", "https://user@example.org", "https://@example.org",
		" https://example.org", "https://example.org/ ", "https://example.org/?x=a b",
		"https://example.org/\n", "https://example.org/\x00", "https://example.org/\u00a0",
		"https://example.org\\@evil.org", "https:\\example.org", "https://example.org/path\\part",
		"https://example.org/%ZZ", "http://[bad]/", "http://example.org:",
		"http://example.org:0", "http://example.org:65536", "http://example.org:abc",
		"https://example.org/\xff",
	} {
		t.Run(raw, func(t *testing.T) {
			err := ValidateURL(raw)
			var validation *ValidationError
			if !errors.As(err, &validation) || validation.Field != "url" {
				t.Fatalf("expected URL validation error, got %v", err)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("error exposes URL credentials")
			}
		})
	}
}

func TestValidateAlias(t *testing.T) {
	for _, alias := range []string{"abc", "A_9-z", strings.Repeat("a", 32)} {
		if err := ValidateAlias(alias); err != nil {
			t.Fatal(err)
		}
	}
	for _, alias := range []string{"", "ab", strings.Repeat("a", 33), "a.b", "a/b", "код", "a b", "abc\n"} {
		var validation *ValidationError
		if err := ValidateAlias(alias); !errors.As(err, &validation) || validation.Field != "alias" {
			t.Fatalf("alias=%q error=%v", alias, err)
		}
	}
}
