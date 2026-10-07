package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"search-router/internal/model"
)

// Serper is the Google SERP adapter (contentType=abstract).
type Serper struct {
	id       string
	client   *http.Client
	endpoint string
}

func NewSerper(cfg model.ProviderConfig) *Serper {
	return &Serper{id: cfg.ID, client: http.DefaultClient, endpoint: "https://google.serper.dev/search"}
}

func (s *Serper) ID() string          { return s.id }
func (s *Serper) ContentType() string { return model.ContentTypeAbstract }
func (s *Serper) Capabilities() []string {
	return []string{"page", "country", "lang", "timeRange"}
}

func (s *Serper) Search(ctx context.Context, req model.SearchRequest, key model.Key) ([]model.SearchResult, error) {
	body := map[string]interface{}{"q": req.Query}
	if req.Page > 0 {
		body["page"] = req.Page
	}
	if req.Country != "" {
		body["gl"] = req.Country
	}
	if req.Lang != "" {
		body["hl"] = req.Lang
	}
	if req.TimeRange != "" {
		body["tbs"] = req.TimeRange
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, networkError(err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, networkError(err)
	}
	httpReq.Header.Set("X-API-KEY", key.Value)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(httpReq)
	if err != nil {
		return nil, networkError(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, classifyError(resp.StatusCode, data, resp.Header.Get("Retry-After"))
	}
	return parseSerper(data)
}

type serperResponse struct {
	Organic []struct {
		Title   string `json:"title"`
		Link    string `json:"link"`
		Snippet string `json:"snippet"`
		Date    string `json:"date"`
	} `json:"organic"`
}

func parseSerper(data []byte) ([]model.SearchResult, error) {
	var r serperResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, networkError(err)
	}
	results := make([]model.SearchResult, 0, len(r.Organic))
	for _, o := range r.Organic {
		res := model.SearchResult{
			Title:       o.Title,
			URL:         o.Link,
			Content:     o.Snippet,
			ContentType: model.ContentTypeAbstract,
		}
		if o.Date != "" {
			d := o.Date
			res.PublishedDate = &d
		}
		results = append(results, res)
	}
	return results, nil
}
