// Package response writes JSON success and error responses.
package response

import (
	"encoding/json"
	"net/http"
)

// ErrorResponse holds an API error and its request ID.
type ErrorResponse struct {
	Error     APIError `json:"error"`
	RequestID string   `json:"request_id"`
}

// APIError holds a stable error code, message and optional field.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
}

// Error writes an API error with the supplied request ID.
func Error(w http.ResponseWriter, requestID string, status int, code, message, field string) {
	JSON(w, status, ErrorResponse{
		Error: APIError{Code: code, Message: message, Field: field}, RequestID: requestID,
	})
}

// JSON writes a JSON response with caching disabled.
func JSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
