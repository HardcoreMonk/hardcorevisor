package api

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// APIError is a structured error response.
type APIError struct {
	Error   string `json:"error"`
	Code    string `json:"code"`
	Details string `json:"details,omitempty"`
}

// Common error codes
const (
	ErrCodeBadRequest   = "BAD_REQUEST"
	ErrCodeNotFound     = "NOT_FOUND"
	ErrCodeConflict     = "CONFLICT"
	ErrCodeInternal     = "INTERNAL_ERROR"
	ErrCodeUnauthorized = "UNAUTHORIZED"
	ErrCodeForbidden    = "FORBIDDEN"
)

func writeError(w http.ResponseWriter, status int, code, message string, details ...string) {
	apiErr := APIError{
		Error: message,
		Code:  code,
	}
	if len(details) > 0 {
		apiErr.Details = details[0]
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(apiErr)
}

// Validation helpers
func validateRequired(fields map[string]string) string {
	for field, value := range fields {
		if value == "" {
			return field + " is required"
		}
	}
	return ""
}

func validateRange(field string, value, min, max int) string {
	if value < min || value > max {
		return fmt.Sprintf("%s must be between %d and %d, got %d", field, min, max, value)
	}
	return ""
}
