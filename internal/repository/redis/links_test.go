package redis

import (
	"strings"
	"testing"
)

func TestDecodeLink(t *testing.T) {
	link, err := decodeLink([]byte(`{"id":9223372036854775807,"url":"https://example.org/путь"}`))
	if err != nil || link.ID != 9223372036854775807 || link.URL != "https://example.org/путь" {
		t.Fatalf("link=%+v err=%v", link, err)
	}
	for _, data := range []string{
		`broken`, `null`, `{}`, `[]`, `{"id":0,"url":"https://example.org"}`,
		`{"id":-1,"url":"https://example.org"}`, `{"id":1}`, `{"id":"1","url":"x"}`,
		`{"id":1.5,"url":"x"}`, `{"id":9223372036854775808,"url":"x"}`,
		`{"id":1,"url":"x","extra":true}`, `{"id":1,"url":"x"} {}`,
		`{"id":1,"url":"` + strings.Repeat("a", 2049) + `"}`,
		"{\"id\":1,\"url\":\"\xff\"}", strings.Repeat("a", (16<<10)+1),
	} {
		if _, err := decodeLink([]byte(data)); err == nil {
			t.Fatalf("accepted corrupt payload %q", data)
		}
	}
}
