package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"search-router/internal/model"
)

type fakeSearcher struct {
	resp *model.SearchResponse
	err  error
}

func (f *fakeSearcher) Search(_ context.Context, _ model.SearchRequest) (*model.SearchResponse, error) {
	return f.resp, f.err
}

func doReq(t *testing.T, s Searcher, body string) *httptest.ResponseRecorder {
	t.Helper()
	handler := NewHandler(s)
	req := httptest.NewRequest(http.MethodPost, "/search", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func TestA9_Status200(t *testing.T) {
	resp := &model.SearchResponse{
		Results: []model.SearchResult{{Title: "t", URL: "https://u", Content: "c", ContentType: model.ContentTypeAbstract}},
		Meta:    model.Meta{Provider: "serper", KeyID: "k1", TookMs: 5},
	}
	w := doReq(t, &fakeSearcher{resp: resp}, `{"query":"golang"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", w.Code)
	}
	var got model.SearchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("response JSON: %v", err)
	}
	if len(got.Results) != 1 || got.Results[0].Title != "t" {
		t.Fatalf("results: %+v", got.Results)
	}
	if got.Meta.Provider != "serper" || got.Meta.KeyID != "k1" {
		t.Fatalf("meta: %+v", got.Meta)
	}
}

func TestA9_Status502(t *testing.T) {
	w := doReq(t, &fakeSearcher{err: &model.AllProvidersFailedError{}}, `{"query":"golang"}`)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status: got %d want 502", w.Code)
	}
}

func TestA9_Status400_BadRequest(t *testing.T) {
	w := doReq(t, &fakeSearcher{err: &model.ProviderError{Kind: model.KindBadRequest}}, `{"query":"golang"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400", w.Code)
	}
}

func TestA9_Status400_MissingQuery(t *testing.T) {
	w := doReq(t, &fakeSearcher{}, `{}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400 (missing query)", w.Code)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	handler := NewHandler(&fakeSearcher{})
	req := httptest.NewRequest(http.MethodGet, "/search", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status: got %d want 405", w.Code)
	}
}
