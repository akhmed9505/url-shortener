package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/akhmed9505/url-shortener/internal/config"
	"github.com/akhmed9505/url-shortener/internal/domain"
	"github.com/akhmed9505/url-shortener/internal/service/shortener"
)

type Handler struct {
	shortener *shortener.Service
	baseURL   string
	log       *slog.Logger
}

func NewHandler(service *shortener.Service, baseURL string, log *slog.Logger) *Handler {
	return &Handler{shortener: service, baseURL: baseURL, log: log}
}

type ShortenRequest struct {
	URL   string  `json:"url"`
	Alias *string `json:"alias,omitempty"`
}

type ShortenResponse struct {
	Code      string    `json:"code"`
	URL       string    `json:"url"`
	ShortURL  string    `json:"short_url"`
	CreatedAt time.Time `json:"created_at"`
}

func (h *Handler) Shorten(w http.ResponseWriter, r *http.Request) {
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || len(r.Header.Values("Content-Type")) != 1 || mediaType != "application/json" {
		writeError(w, r, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json", "")
		return
	}
	for key, value := range params {
		if key != "charset" || !strings.EqualFold(value, "utf-8") {
			writeError(w, r, http.StatusUnsupportedMediaType, "unsupported_media_type", "Only UTF-8 JSON is supported", "")
			return
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, r, http.StatusRequestEntityTooLarge, "body_too_large", "Request body must not exceed 4096 bytes", "")
		} else {
			writeError(w, r, http.StatusBadRequest, "invalid_json", "Cannot read JSON request body", "")
		}
		return
	}
	request, err := decodeShortenRequest(body)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_json", "Expected one JSON object with string url and optional string alias", "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), config.HTTPRequestTimeout)
	defer cancel()
	link, err := h.shortener.Create(ctx, request.URL, request.Alias)
	if err != nil {
		h.operationError(w, r, err)
		return
	}
	shortURL := h.baseURL + "/s/" + link.Code
	w.Header().Set("Location", shortURL)
	writeJSON(w, http.StatusCreated, ShortenResponse{
		Code: link.Code, URL: link.OriginalURL, ShortURL: shortURL, CreatedAt: link.CreatedAt.UTC(),
	})
}

func decodeShortenRequest(body []byte) (ShortenRequest, error) {
	var request ShortenRequest
	if !utf8.Valid(body) {
		return request, errors.New("invalid UTF-8")
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return request, errors.New("expected object")
	}
	seen := make(map[string]bool, 2)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return request, err
		}
		key, ok := token.(string)
		if !ok || seen[key] || (key != "url" && key != "alias") {
			return request, errors.New("unknown or duplicate field")
		}
		seen[key] = true
		var value *string
		if err := decoder.Decode(&value); err != nil || value == nil {
			return request, errors.New("expected string field")
		}
		if key == "url" {
			request.URL = *value
		} else {
			request.Alias = value
		}
	}
	if _, err := decoder.Token(); err != nil {
		return request, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return request, errors.New("trailing JSON data")
	}
	return request, nil
}

func (h *Handler) operationError(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() != nil {
		return
	}
	var validation *shortener.ValidationError
	switch {
	case errors.As(err, &validation):
		writeError(w, r, http.StatusBadRequest, "invalid_"+validation.Field, validation.Message, validation.Field)
	case errors.Is(err, domain.ErrCodeConflict):
		writeError(w, r, http.StatusConflict, "alias_taken", "Alias is already in use", "alias")
	case errors.Is(err, domain.ErrLinkNotFound):
		writeError(w, r, http.StatusNotFound, "link_not_found", "Link not found", "")
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, r, http.StatusGatewayTimeout, "request_timeout", "Request timed out", "")
	default:
		status, code, message := http.StatusInternalServerError, "internal_error", "Internal server error"
		if errors.Is(err, domain.ErrStorageUnavailable) {
			status, code, message = http.StatusServiceUnavailable, "storage_unavailable", "Storage is temporarily unavailable"
		} else if errors.Is(err, shortener.ErrCodeGenerationExhausted) {
			code = "code_generation_exhausted"
		}
		h.log.Error("shortener operation failed", "request_id", requestID(r), "error_code", code)
		writeError(w, r, status, code, message, "")
	}
}
