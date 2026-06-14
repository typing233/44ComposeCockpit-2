package httputil

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
)

type Response struct {
	Data interface{} `json:"data,omitempty"`
	Meta *Meta       `json:"meta"`
}

type ErrorResponse struct {
	Error *ErrorBody `json:"error"`
	Meta  *Meta      `json:"meta"`
}

type ErrorBody struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

type Meta struct {
	RequestID string `json:"request_id"`
	Timestamp string `json:"timestamp"`
}

type PaginatedResponse struct {
	Data       interface{} `json:"data"`
	Meta       *Meta       `json:"meta"`
	Pagination *Pagination `json:"pagination"`
}

type Pagination struct {
	Total  int `json:"total"`
	Offset int `json:"offset"`
	Limit  int `json:"limit"`
}

func newMeta(requestID string) *Meta {
	return &Meta{
		RequestID: requestID,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
}

func WriteJSON(w http.ResponseWriter, r *http.Request, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	resp := Response{
		Data: data,
		Meta: newMeta(GetRequestID(r)),
	}
	json.NewEncoder(w).Encode(resp)
}

func WritePaginated(w http.ResponseWriter, r *http.Request, data interface{}, total, offset, limit int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	resp := PaginatedResponse{
		Data: data,
		Meta: newMeta(GetRequestID(r)),
		Pagination: &Pagination{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	}
	json.NewEncoder(w).Encode(resp)
}

func WriteError(w http.ResponseWriter, r *http.Request, status int, code, message string, details interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	resp := ErrorResponse{
		Error: &ErrorBody{
			Code:    code,
			Message: message,
			Details: details,
		},
		Meta: newMeta(GetRequestID(r)),
	}
	json.NewEncoder(w).Encode(resp)
}

func GetRequestID(r *http.Request) string {
	if id := r.Header.Get("X-Request-ID"); id != "" {
		return id
	}
	return uuid.New().String()
}
