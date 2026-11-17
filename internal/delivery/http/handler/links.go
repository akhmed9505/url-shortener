package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/akhmed9505/url-shortener/internal/config"
	"github.com/akhmed9505/url-shortener/internal/delivery/http/middleware"
	httpresponse "github.com/akhmed9505/url-shortener/internal/delivery/http/response"
	"github.com/go-chi/chi/v5"
)

// Shorten creates a short link from a strictly validated JSON request.
func (h *Handler) Shorten(w http.ResponseWriter, r *http.Request) {
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || len(r.Header.Values("Content-Type")) != 1 || mediaType != "application/json" {
		httpresponse.Error(w, middleware.RequestID(r), http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json", "")
		return
	}
	for key, value := range params {
		if key != "charset" || !strings.EqualFold(value, "utf-8") {
			httpresponse.Error(w, middleware.RequestID(r), http.StatusUnsupportedMediaType, "unsupported_media_type", "Only UTF-8 JSON is supported", "")
			return
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpresponse.Error(w, middleware.RequestID(r), http.StatusRequestEntityTooLarge, "body_too_large", "Request body must not exceed 4096 bytes", "")
		} else {
			httpresponse.Error(w, middleware.RequestID(r), http.StatusBadRequest, "invalid_json", "Cannot read JSON request body", "")
		}
		return
	}
	request, err := decodeShortenRequest(body)
	if err != nil {
		httpresponse.Error(w, middleware.RequestID(r), http.StatusBadRequest, "invalid_json", "Expected one JSON object with string url and optional string alias", "")
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
	httpresponse.JSON(w, http.StatusCreated, ShortenResponse{
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

// Redirect redirects to a stored destination and records the visit.
func (h *Handler) Redirect(w http.ResponseWriter, r *http.Request) {
	visitedAt := time.Now().UTC()
	ctx, cancel := context.WithTimeout(r.Context(), config.HTTPRequestTimeout)
	defer cancel()
	link, err := h.shortener.Resolve(ctx, chi.URLParam(r, "code"), r.UserAgent(), visitedAt)
	if err != nil {
		h.operationError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, link.OriginalURL, http.StatusFound)
}
