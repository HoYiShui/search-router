package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"search-router/internal/model"
)

// tavilyMaxResults is the gateway-fixed result count for Tavily.
const tavilyMaxResults = 5

// Tavily is the AI search adapter (contentType=body).
type Tavily struct {
	id       string
	client   *http.Client
	endpoint string
}

func NewTavily(cfg model.ProviderConfig) *Tavily {
	return &Tavily{id: cfg.ID, client: http.DefaultClient, endpoint: "https://api.tavily.com/search"}
}

func (t *Tavily) ID() string          { return t.id }
func (t *Tavily) ContentType() string { return model.ContentTypeBody }
func (t *Tavily) Capabilities() []string {
	// tavily 无翻页，不支持 page；仅支持 timeRange。
	return []string{"timeRange"}
}

func (t *Tavily) Search(ctx context.Context, req model.SearchRequest, key model.Key) ([]model.SearchResult, error) {
	body := map[string]interface{}{
		"query":       req.Query,
		"max_results": tavilyMaxResults,
	}
	if req.TimeRange != "" {
		body["days"] = req.TimeRange
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, networkError(err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, networkError(err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+key.Value)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(httpReq)
	if err != nil {
		return nil, networkError(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, classifyError(resp.StatusCode, data, resp.Header.Get("Retry-After"))
	}
	return parseTavily(data)
}

type tavilyResponse struct {
	Results []struct {
		Title         string   `json:"title"`
		URL           string   `json:"url"`
		Content       string   `json:"content"`
		Score         *float64 `json:"score"`
		PublishedDate *string  `json:"published_date"`
	} `json:"results"`
}

func parseTavily(data []byte) ([]model.SearchResult, error) {
	var r tavilyResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, networkError(err)
	}
	results := make([]model.SearchResult, 0, len(r.Results))
	for _, o := range r.Results {
		results = append(results, model.SearchResult{
			Title:         o.Title,
			URL:           o.URL,
			Content:       o.Content,
			ContentType:   model.ContentTypeBody,
			Score:         o.Score,
			PublishedDate: o.PublishedDate,
		})
	}
	return results, nil
}
