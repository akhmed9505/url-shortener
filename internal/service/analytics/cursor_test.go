package analytics

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/akhmed9505/url-shortener/internal/domain"
)

func cursorTestPeriod() domain.AnalyticsPeriod {
	return domain.AnalyticsPeriod{
		From: time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2024, 3, 2, 0, 0, 0, 0, time.UTC),
	}
}

func TestCursorRoundTrip(t *testing.T) {
	period := cursorTestPeriod()
	visit := domain.Visit{ID: 9223372036854775807, VisitedAt: period.From.Add(123456 * time.Microsecond)}
	raw, err := encodeCursor("abc", period, visit)
	if err != nil {
		t.Fatal(err)
	}
	position, err := decodeCursor(raw, "abc", period)
	if err != nil || position.ID != visit.ID || !position.VisitedAt.Equal(visit.VisitedAt) || position.VisitedAt.Location() != time.UTC {
		t.Fatalf("position=%+v err=%v", position, err)
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || strings.Contains(raw, "=") || len(raw) > 1024 {
		t.Fatalf("raw=%q err=%v", raw, err)
	}

	// The encoded size limit includes the complete Base64URL string.
	paddedJSON := append(append([]byte(nil), data...), []byte(strings.Repeat(" ", 768-len(data)))...)
	boundary := base64.RawURLEncoding.EncodeToString(paddedJSON)
	if len(boundary) != 1024 {
		t.Fatalf("cursor length=%d", len(boundary))
	}
	if _, err := decodeCursor(boundary, "abc", period); err != nil {
		t.Fatalf("1024-character cursor rejected: %v", err)
	}
	var cursor visitCursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		t.Fatal(err)
	}
	if cursor.LastVisitedAt != "2024-02-29T00:00:00.123456Z" || cursor.LastID != "9223372036854775807" {
		t.Fatalf("cursor=%+v", cursor)
	}
}

func TestInvalidCursors(t *testing.T) {
	period := cursorTestPeriod()
	valid := map[string]any{
		"v": 1, "code": "abc", "from": "2024-02-29", "to": "2024-03-02",
		"last_visited_at": "2024-02-29T12:00:00.000000Z", "last_id": "1",
	}
	for _, tc := range []struct {
		name, field string
		value       any
	}{
		{"version", "v", 2}, {"version type", "v", "1"}, {"version null", "v", nil},
		{"code", "code", "other"}, {"code null", "code", nil},
		{"zero id", "last_id", "0"}, {"negative id", "last_id", "-1"},
		{"overflow id", "last_id", "9223372036854775808"}, {"numeric id", "last_id", 1},
		{"null id", "last_id", nil}, {"leading zero id", "last_id", "01"},
		{"signed id", "last_id", "+1"}, {"fractional id", "last_id", "1.0"},
		{"from mismatch", "from", "2024-02-28"}, {"to mismatch", "to", "2024-03-03"},
		{"invalid date", "from", "2023-02-29"}, {"excessive period", "to", "2026-03-02"},
		{"zero id", "last_id", "0"}, {"negative id", "last_id", "-1"},
		{"overflow id", "last_id", "9223372036854775808"}, {"numeric id", "last_id", 1},
		{"leading zero", "last_id", "01"}, {"signed id", "last_id", "+1"},
		{"spaces in id", "last_id", " 1"}, {"id null", "last_id", nil},
		{"before period", "last_visited_at", "2024-02-28T23:59:59.999999Z"},
		{"exclusive end", "last_visited_at", "2024-03-02T00:00:00.000000Z"},
		{"offset", "last_visited_at", "2024-02-29T12:00:00.000000+00:00"},
		{"nanoseconds", "last_visited_at", "2024-02-29T12:00:00.000000001Z"},
		{"milliseconds", "last_visited_at", "2024-02-29T12:00:00.000Z"},
		{"no fraction", "last_visited_at", "2024-02-29T12:00:00Z"},
		{"impossible day", "last_visited_at", "2024-02-30T12:00:00.000000Z"},
		{"timestamp null", "last_visited_at", nil}, {"unknown field", "extra", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := make(map[string]any, len(valid))
			for key, value := range valid {
				payload[key] = value
			}
			payload[tc.field] = tc.value
			data, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			assertInvalidCursor(t, base64.RawURLEncoding.EncodeToString(data), period)
		})
	}
	data, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"", "%%%", strings.Repeat("A", 1025), base64.RawURLEncoding.EncodeToString(data) + "=", base64.RawURLEncoding.EncodeToString(data) + "\n"} {
		assertInvalidCursor(t, raw, period)
	}
	for _, data := range []string{
		`null`, `[]`, `{}`, `{"v":1}`, `{"v":1,"v":1}`, string(data) + ` {}`,
		strings.Replace(string(data), `"v":1`, `"V":1`, 1),
		strings.Replace(string(data), `"v":1`, `"v":1,"v":1`, 1),
		strings.Replace(string(data), `"code":"abc"`, `"code":"\xff"`, 1),
		strings.Replace(string(data), "abc", string([]byte{0xff}), 1),
	} {
		assertInvalidCursor(t, base64.RawURLEncoding.EncodeToString([]byte(data)), period)
	}
	for field := range valid {
		payload := make(map[string]any, len(valid))
		for key, value := range valid {
			if key != field {
				payload[key] = value
			}
		}
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		assertInvalidCursor(t, base64.RawURLEncoding.EncodeToString(data), period)
	}
}

func assertInvalidCursor(t *testing.T, raw string, period domain.AnalyticsPeriod) {
	t.Helper()
	_, err := decodeCursor(raw, "abc", period)
	var validation *domain.ValidationError
	if !errors.As(err, &validation) || validation.Field != "cursor" {
		t.Fatalf("expected invalid cursor, got %v", err)
	}
}
