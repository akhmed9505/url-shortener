//go:build integration

package integration

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/akhmed9505/url-shortener/internal/config"
)

func TestApplicationServesEmbeddedInterface(t *testing.T) {
	pool, _ := newDatabase(t, true)
	state := startApplication(t, pool, config.Redis{Addr: "127.0.0.1:1"})
	client := &http.Client{}
	for _, tc := range []struct{ path, contentType, content string }{
		{"/", "text/html", "Сокращение ссылок"},
		{"/assets/app.css", "text/css", "overflow-x: auto"},
		{"/assets/app.js", "text/javascript", "next_cursor"},
	} {
		response, err := client.Get(state.url + tc.path)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), tc.contentType) || !strings.Contains(string(body), tc.content) {
			t.Fatalf("GET %s: status=%d type=%q", tc.path, response.StatusCode, response.Header.Get("Content-Type"))
		}
	}
	// Static routing must leave the existing dependency-independent liveness intact.
	response, err := client.Get(state.url + "/health/live")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("liveness = %d", response.StatusCode)
	}
}
