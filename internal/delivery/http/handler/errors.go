package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/akhmed9505/url-shortener/internal/delivery/http/middleware"
	httpresponse "github.com/akhmed9505/url-shortener/internal/delivery/http/response"
	"github.com/akhmed9505/url-shortener/internal/domain"
	"github.com/akhmed9505/url-shortener/internal/service/shortener"
)

func (h *Handler) operationError(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() != nil {
		return
	}
	var validation *domain.ValidationError
	switch {
	case errors.As(err, &validation):
		httpresponse.Error(w, middleware.RequestID(r), http.StatusBadRequest, "invalid_"+validation.Field, validation.Message, validation.Field)
	case errors.Is(err, domain.ErrCodeConflict):
		httpresponse.Error(w, middleware.RequestID(r), http.StatusConflict, "alias_taken", "Alias is already in use", "alias")
	case errors.Is(err, domain.ErrLinkNotFound):
		httpresponse.Error(w, middleware.RequestID(r), http.StatusNotFound, "link_not_found", "Link not found", "")
	case errors.Is(err, context.DeadlineExceeded):
		httpresponse.Error(w, middleware.RequestID(r), http.StatusGatewayTimeout, "request_timeout", "Request timed out", "")
	default:
		status, code, message := http.StatusInternalServerError, "internal_error", "Internal server error"
		if errors.Is(err, domain.ErrStorageUnavailable) {
			status, code, message = http.StatusServiceUnavailable, "storage_unavailable", "Storage is temporarily unavailable"
		} else if errors.Is(err, shortener.ErrCodeGenerationExhausted) {
			code = "code_generation_exhausted"
		}
		h.log.Error("shortener operation failed", "request_id", middleware.RequestID(r), "error_code", code)
		httpresponse.Error(w, middleware.RequestID(r), status, code, message, "")
	}
}
