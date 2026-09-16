package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/chavaliadi/watchdog/internal/service"
)

type ErrorEnvelope struct {
	Error ErrorPayload `json:"error"`
}

type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details"`
}

func writeJSON(w http.ResponseWriter, statusCode int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, statusCode int, code string, message string, details any) {
	writeJSON(w, statusCode, ErrorEnvelope{
		Error: ErrorPayload{
			Code:    code,
			Message: message,
			Details: details,
		},
	})
}

func mapServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrNotFound):
		writeError(w, http.StatusNotFound, "NOT_FOUND", "the requested resource was not found", nil)
	case errors.Is(err, service.ErrInvalidID):
		writeError(w, http.StatusBadRequest, "INVALID_ID", "the provided id is not a valid uuid", nil)
	case errors.Is(err, service.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error(), nil)
	case errors.Is(err, service.ErrConflict):
		writeError(w, http.StatusConflict, "ALREADY_EXISTS", err.Error(), nil)
	default:
		// Never leak raw PostgreSQL/internal details
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "an internal operational failure occurred", nil)
	}
}
