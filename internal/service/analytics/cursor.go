package analytics

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/akhmed9505/url-shortener/internal/domain"
)

const cursorTimeLayout = "2006-01-02T15:04:05.000000Z"

var errInvalidCursor = &domain.ValidationError{Field: "cursor", Message: "Invalid cursor"}

type visitCursor struct {
	Version       int    `json:"v"`
	Code          string `json:"code"`
	From          string `json:"from"`
	To            string `json:"to"`
	LastVisitedAt string `json:"last_visited_at"`
	LastID        string `json:"last_id"`
}

func encodeCursor(code string, period domain.AnalyticsPeriod, visit domain.Visit) (string, error) {
	data, err := json.Marshal(visitCursor{
		Version: 1, Code: code,
		From: period.From.Format(time.DateOnly), To: period.To.Format(time.DateOnly),
		LastVisitedAt: visit.VisitedAt.UTC().Format(cursorTimeLayout), LastID: strconv.FormatInt(visit.ID, 10),
	})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeCursor(raw, code string, period domain.AnalyticsPeriod) (domain.VisitPosition, error) {
	var position domain.VisitPosition
	if len(raw) == 0 || len(raw) > 1024 {
		return position, errInvalidCursor
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil || base64.RawURLEncoding.EncodeToString(data) != raw || !utf8.Valid(data) {
		return position, errInvalidCursor
	}
	cursor, err := decodeCursorJSON(data)
	if err != nil || cursor.Version != 1 || cursor.Code != code ||
		cursor.From != period.From.Format(time.DateOnly) || cursor.To != period.To.Format(time.DateOnly) {
		return position, errInvalidCursor
	}
	id, err := strconv.ParseInt(cursor.LastID, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != cursor.LastID {
		return position, errInvalidCursor
	}
	visitedAt, err := time.Parse(cursorTimeLayout, cursor.LastVisitedAt)
	if err != nil || visitedAt.Year() < 1 || visitedAt.Format(cursorTimeLayout) != cursor.LastVisitedAt ||
		visitedAt.Before(period.From) || !visitedAt.Before(period.To) {
		return position, errInvalidCursor
	}
	return domain.VisitPosition{ID: id, VisitedAt: visitedAt}, nil
}

func decodeCursorJSON(data []byte) (visitCursor, error) {
	var cursor visitCursor
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return cursor, errInvalidCursor
	}
	seen := make(map[string]bool, 6)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return cursor, errInvalidCursor
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return cursor, errInvalidCursor
		}
		seen[key] = true
		var target any
		switch key {
		case "v":
			target = &cursor.Version
		case "code":
			target = &cursor.Code
		case "from":
			target = &cursor.From
		case "to":
			target = &cursor.To
		case "last_visited_at":
			target = &cursor.LastVisitedAt
		case "last_id":
			target = &cursor.LastID
		default:
			return cursor, errInvalidCursor
		}
		if err := decoder.Decode(target); err != nil {
			return cursor, errInvalidCursor
		}
	}
	if _, err := decoder.Token(); err != nil || len(seen) != 6 {
		return cursor, errInvalidCursor
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return cursor, errInvalidCursor
	}
	return cursor, nil
}
