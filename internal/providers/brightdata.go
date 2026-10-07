package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"search-router/internal/model"
)

// BrightData is the Bright Data SERP adapter (contentType=abstract). Unlike the
// direct query APIs, it constructs a Google SERP URL and proxies it through the
// Bright Data request endpoint, using the configured zone.
type BrightData struct {
	id       string
	zone     string
	client   *http.Client
	endpoint string
}

func NewBrightData(cfg model.ProviderConfig) *BrightData {
	return &BrightData{id: cfg.ID, zone: cfg.Zone, client: http.DefaultClient, endpoint: "https://api.brightdata.com/request"}
}

func (b *BrightData) ID() string          { return b.id }
func (b *BrightData) ContentType() string { return model.ContentTypeAbstract }
func (b *BrightData) Capabilities() []string {
	return []string{"page", "country", "lang", "timeRange"}
}

func (b *BrightData) Search(ctx context.Context, req model.SearchRequest, key model.Key) ([]model.SearchResult, error) {
	serpURL := buildGoogleURL(req)

	body := map[string]interface{}{
		"zone":        b.zone,
		"url":         serpURL,
		"format":      "json",
		"data_format": "parsed",
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, networkError(err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, b.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, networkError(err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+key.Value)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := b.client.Do(httpReq)
	if err != nil {
		return nil, networkError(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, classifyError(resp.StatusCode, data, resp.Header.Get("Retry-After"))
	}
	return parseBrightData(data)
}

// buildGoogleURL constructs a google.com/search URL from the gateway request:
// Query→q, Page→start, Country→gl, Lang→hl, TimeRange→tbs.
func buildGoogleURL(req model.SearchRequest) string {
	u, _ := url.Parse("https://www.google.com/search")
	q := u.Query()
	q.Set("q", req.Query)
	if req.Page > 1 {
		q.Set("start", strconv.Itoa((req.Page-1)*10))
	}
	if req.Country != "" {
		q.Set("gl", req.Country)
	}
	if req.Lang != "" {
		q.Set("hl", req.Lang)
	}
	if req.TimeRange != "" {
		q.Set("tbs", req.TimeRange)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

type brightDataResponse struct {
	Organic []struct {
		Title       string `json:"title"`
		Link        string `json:"link"`
		Description string `json:"description"`
		Snippet     string `json:"snippet"`
	} `json:"organic"`
}

func parseBrightData(data []byte) ([]model.SearchResult, error) {
	var r brightDataResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, networkError(err)
	}
	results := make([]model.SearchResult, 0, len(r.Organic))
	for _, o := range r.Organic {
		content := o.Description
		if content == "" {
			content = o.Snippet
		}
		results = append(results, model.SearchResult{
			Title:       o.Title,
			URL:         o.Link,
			Content:     content,
			ContentType: model.ContentTypeAbstract,
		})
	}
	return results, nil
}
