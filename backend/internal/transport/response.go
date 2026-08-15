package transport

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

type envelope map[string]any

func writeJSON(w http.ResponseWriter, status int, data any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(data)
}

func errorJSON(w http.ResponseWriter, status int, message string) {
	type errorResponse struct {
		Error string `json:"error"`
	}

	resp := errorResponse{Error: message}
	_ = writeJSON(w, status, resp)
}

func readJSON(r *http.Request, dst any) error {
	// Limit set at 1MB (can be modified)
	r.Body = http.MaxBytesReader(nil, r.Body, 1_048_576)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	err := dec.Decode(dst)
	if err != nil {
		return err
	}

	err = dec.Decode(&struct{}{})
	if !errors.Is(err, io.EOF) {
		return errors.New("body must only contain a single JSON object")
	}

	return nil
}
