package main

import (
	"encoding/json"
	"net/http"
)

var (
	ErrURLNotFound     = err("url not found")
	ErrShortCodeConflict = err("short code already taken")
	ErrNotOwner        = err("not owner")
	ErrURLGone        = err("url gone")
)

type err string

func (e err) Error() string { return string(e) }

type errorResponse struct {
	Error string `json:"error"`
}

type fieldErrorResponse struct {
	Error string `json:"error"`
	Field string `json:"field,omitempty"`
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(errorResponse{Error: message})
}

func writeFieldError(w http.ResponseWriter, status int, message, field string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(fieldErrorResponse{Error: message, Field: field})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
