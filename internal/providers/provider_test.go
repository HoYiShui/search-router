package providers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"search-router/internal/model"
)

func TestA10_SerperMapping(t *testing.T) {
	sample := []byte(`{"organic":[{"title":"Go (programming language)","link":"https://go.dev/","snippet":"Go is an open source programming language","date":"2024-03-05"}]}`)
	results, err := parseSerper(sample)
	if err != nil {
		t.Fatalf("parseSerper: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len: got %d want 1", len(results))
	}
	r := results[0]
	if r.Title != "Go (programming language)" {
		t.Errorf("title: got %q", r.Title)
	}
	if r.URL != "https://go.dev/" {
		t.Errorf("url: got %q", r.URL)
	}
	if r.Content != "Go is an open source programming language" {
		t.Errorf("content: got %q", r.Content)
	}
	if r.ContentType != model.ContentTypeAbstract {
		t.Errorf("contentType: got %q want %q", r.ContentType, model.ContentTypeAbstract)
	}
	if r.Score != nil {
		t.Errorf("score: serper 应无 score，got %v", *r.Score)
	}
	if r.PublishedDate == nil || *r.PublishedDate != "2024-03-05" {
		t.Errorf("publishedDate: got %v want 2024-03-05", r.PublishedDate)
	}
}

func TestA10_TavilyMapping(t *testing.T) {
	sample := []byte(`{"results":[{"title":"t1","url":"https://x","content":"full body","score":0.87,"published_date":"2024-03-05"}]}`)
	results, err := parseTavily(sample)
	if err != nil {
		t.Fatalf("parseTavily: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len: got %d want 1", len(results))
	}
	r := results[0]
	if r.ContentType != model.ContentTypeBody {
		t.Errorf("contentType: got %q want %q", r.ContentType, model.ContentTypeBody)
	}
	if r.Score == nil || *r.Score != 0.87 {
		t.Errorf("score: got %v want 0.87", r.Score)
	}
	if r.PublishedDate == nil || *r.PublishedDate != "2024-03-05" {
		t.Errorf("publishedDate: got %v want 2024-03-05", r.PublishedDate)
	}
	if r.Content != "full body" {
		t.Errorf("content: got %q", r.Content)
	}
}

func TestA10_BraveMapping(t *testing.T) {
	sample := []byte(`{"web":{"results":[{"title":"t","url":"https://u","description":"d","age":"2024-03-05"}]}}`)
	results, err := parseBrave(sample)
	if err != nil {
		t.Fatalf("parseBrave: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len: got %d want 1", len(results))
	}
	r := results[0]
	if r.ContentType != model.ContentTypeAbstract {
		t.Errorf("contentType: got %q want %q", r.ContentType, model.ContentTypeAbstract)
	}
	if r.Content != "d" {
		t.Errorf("content: got %q want d", r.Content)
	}
	if r.PublishedDate == nil || *r.PublishedDate != "2024-03-05" {
		t.Errorf("publishedDate: got %v want 2024-03-05", r.PublishedDate)
	}
}

func TestA10_BrightDataMapping(t *testing.T) {
	sample := []byte(`{"organic":[{"title":"t","link":"https://u","description":"d","snippet":"s"}]}`)
	results, err := parseBrightData(sample)
	if err != nil {
		t.Fatalf("parseBrightData: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len: got %d want 1", len(results))
	}
	r := results[0]
	if r.ContentType != model.ContentTypeAbstract {
		t.Errorf("contentType: got %q want %q", r.ContentType, model.ContentTypeAbstract)
	}
	if r.Title != "t" || r.URL != "https://u" || r.Content != "d" {
		t.Errorf("mapping: got title=%q url=%q content=%q", r.Title, r.URL, r.Content)
	}

	// description 缺省时回退 snippet
	fallback := []byte(`{"organic":[{"title":"t","link":"https://u","snippet":"s"}]}`)
	fr, err := parseBrightData(fallback)
	if err != nil {
		t.Fatalf("parseBrightData(fallback): %v", err)
	}
	if fr[0].Content != "s" {
		t.Errorf("fallback content: got %q want s", fr[0].Content)
	}
}

func TestA10_ContentTypePerProvider(t *testing.T) {
	cases := []struct {
		adapter Provider
		want    string
	}{
		{NewSerper(model.ProviderConfig{ID: "serper"}), model.ContentTypeAbstract},
		{NewTavily(model.ProviderConfig{ID: "tavily"}), model.ContentTypeBody},
		{NewBrave(model.ProviderConfig{ID: "brave"}), model.ContentTypeAbstract},
		{NewBrightData(model.ProviderConfig{ID: "brightdata"}), model.ContentTypeAbstract},
	}
	for _, c := range cases {
		if got := c.adapter.ContentType(); got != c.want {
			t.Errorf("%s ContentType: got %q want %q", c.adapter.ID(), got, c.want)
		}
	}
}

func TestSerper_SearchEndToEnd(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("X-API-KEY")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"organic":[{"title":"t","link":"https://u","snippet":"s"}]}`))
	}))
	defer srv.Close()

	s := NewSerper(model.ProviderConfig{ID: "serper"})
	s.endpoint = srv.URL
	s.client = srv.Client()

	results, err := s.Search(context.Background(), model.SearchRequest{Query: "golang"}, model.Key{ID: "k1", Value: "secret"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].Title != "t" {
		t.Fatalf("results: %+v", results)
	}
	if gotAuth != "secret" {
		t.Errorf("X-API-KEY header: got %q want secret", gotAuth)
	}
}

func TestClassifyError_429_RetryAfter(t *testing.T) {
	perr := classifyError(http.StatusTooManyRequests, []byte("rate limited"), "5")
	if perr.Kind != model.KindKeyRateLimited {
		t.Fatalf("kind: got %q want keyRateLimited", perr.Kind)
	}
	if perr.RetryAfter.Seconds() != 5 {
		t.Errorf("retryAfter: got %v want 5s", perr.RetryAfter)
	}
}

func TestClassifyError_Kinds(t *testing.T) {
	cases := []struct {
		status int
		kind   model.ErrorKind
	}{
		{http.StatusUnauthorized, model.KindKeyInvalid},
		{http.StatusForbidden, model.KindKeyInvalid},
		{http.StatusBadRequest, model.KindBadRequest},
		{http.StatusPaymentRequired, model.KindKeyQuotaExhausted},
		{http.StatusInternalServerError, model.KindProviderUnavailable},
		{http.StatusBadGateway, model.KindProviderUnavailable},
	}
	for _, c := range cases {
		perr := classifyError(c.status, nil, "")
		if perr.Kind != c.kind {
			t.Errorf("status %d: got %q want %q", c.status, perr.Kind, c.kind)
		}
	}
}

func TestBuildGoogleURL(t *testing.T) {
	got := buildGoogleURL(model.SearchRequest{Query: "golang", Page: 2, Country: "us", Lang: "en", TimeRange: "w"})
	for _, want := range []string{"q=golang", "start=10", "gl=us", "hl=en", "tbs=w"} {
		if !strings.Contains(got, want) {
			t.Errorf("buildGoogleURL missing %q in %q", want, got)
		}
	}
}
