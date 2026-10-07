package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"search-router/internal/model"
)

// bravePageSize is the fixed page size used to translate gateway page → offset.
const bravePageSize = 10

// Brave is the Brave Search API adapter (contentType=abstract).
type Brave struct {
	id       string
	client   *http.Client
	endpoint string
}

func NewBrave(cfg model.ProviderConfig) *Brave {
	return &Brave{id: cfg.ID, client: http.DefaultClient, endpoint: "https://api.search.brave.com/res/v1/web/search"}
}

func (b *Brave) ID() string          { return b.id }
func (b *Brave) ContentType() string { return model.ContentTypeAbstract }
func (b *Brave) Capabilities() []string {
	return []string{"page", "country", "safeSearch"}
}

func (b *Brave) Search(ctx context.Context, req model.SearchRequest, key model.Key) ([]model.SearchResult, error) {
	u, err := url.Parse(b.endpoint)
	if err != nil {
		return nil, networkError(err)
	}
	q := u.Query()
	q.Set("q", req.Query)
	if req.Page > 0 {
		q.Set("offset", strconv.Itoa((req.Page-1)*bravePageSize))
	}
	if req.Country != "" {
		q.Set("country", req.Country)
	}
	if req.SafeSearch != "" && req.SafeSearch != model.SafeSearchOff {
		q.Set("safesearch", req.SafeSearch)
	}
	u.RawQuery = q.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, networkError(err)
	}
	httpReq.Header.Set("X-Subscription-Token", key.Value)

	resp, err := b.client.Do(httpReq)
	if err != nil {
		return nil, networkError(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, classifyError(resp.StatusCode, data, resp.Header.Get("Retry-After"))
	}
	return parseBrave(data)
}

type braveResponse struct {
	Web struct {
		Results []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
			Age         string `json:"age"`
		} `json:"results"`
	} `json:"web"`
}

func parseBrave(data []byte) ([]model.SearchResult, error) {
	var r braveResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, networkError(err)
	}
	results := make([]model.SearchResult, 0, len(r.Web.Results))
	for _, o := range r.Web.Results {
		res := model.SearchResult{
			Title:       o.Title,
			URL:         o.URL,
			Content:     o.Description,
			ContentType: model.ContentTypeAbstract,
		}
		if o.Age != "" {
			a := o.Age
			res.PublishedDate = &a
		}
		results = append(results, res)
	}
	return results, nil
}
