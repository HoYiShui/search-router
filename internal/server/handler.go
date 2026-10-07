// Package server exposes the single external entrypoint POST /search: JSON
// decode → Searcher.Search → JSON encode, with error-code mapping.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"search-router/internal/model"
)

// Searcher is the narrow dependency of the handler; *router.Router satisfies it.
type Searcher interface {
	Search(ctx context.Context, req model.SearchRequest) (*model.SearchResponse, error)
}

func NewHandler(s Searcher) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		var req model.SearchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}
		if req.Query == "" {
			writeError(w, http.StatusBadRequest, "query required")
			return
		}

		resp, err := s.Search(r.Context(), req)
		if err != nil {
			writeSearchError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	})
	return mux
}

func writeSearchError(w http.ResponseWriter, err error) {
	var perr *model.ProviderError
	if errors.As(err, &perr) && perr.Kind == model.KindBadRequest {
		writeError(w, http.StatusBadRequest, perr.Message)
		return
	}
	var apf *model.AllProvidersFailedError
	if errors.As(err, &apf) {
		writeError(w, http.StatusBadGateway, apf.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
