// Package providers defines the upstream adapter contract and implements
// adapters for Serper, Tavily, Brave, and Bright Data. Each adapter translates
// its provider's request/response shape into the unified model types and
// classifies upstream failures as *model.ProviderError.
package providers

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"search-router/internal/model"
)

// Provider is the adapter contract. Capabilities() is the soft-capability list
// (including "page"); ContentType() is the hard capability ("abstract"|"body").
type Provider interface {
	ID() string
	// 发上游请求；失败时返回 *model.ProviderError（带 Kind），供 router 分类
	Search(ctx context.Context, req model.SearchRequest, key model.Key) ([]model.SearchResult, error)
	Capabilities() []string
	ContentType() string
}

// classifyError maps an upstream HTTP status code to *model.ProviderError.
func classifyError(status int, body []byte, retryAfter string) *model.ProviderError {
	switch {
	case status == http.StatusBadRequest:
		return &model.ProviderError{Kind: model.KindBadRequest, Message: fmt.Sprintf("upstream 400: %s", truncate(body, 200))}
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return &model.ProviderError{Kind: model.KindKeyInvalid, Message: fmt.Sprintf("upstream %d", status)}
	case status == http.StatusTooManyRequests:
		var ra time.Duration
		if s, err := strconv.Atoi(retryAfter); err == nil && s > 0 {
			ra = time.Duration(s) * time.Second
		}
		return &model.ProviderError{Kind: model.KindKeyRateLimited, Message: "429", RetryAfter: ra}
	case status == http.StatusPaymentRequired:
		return &model.ProviderError{Kind: model.KindKeyQuotaExhausted, Message: "upstream quota exhausted"}
	case status >= 500:
		return &model.ProviderError{Kind: model.KindProviderUnavailable, Message: fmt.Sprintf("upstream %d", status)}
	case status >= 400 && status < 500:
		return &model.ProviderError{Kind: model.KindBadRequest, Message: fmt.Sprintf("upstream %d", status)}
	default:
		return &model.ProviderError{Kind: model.KindProviderUnavailable, Message: fmt.Sprintf("unexpected status %d", status)}
	}
}

func truncate(b []byte, n int) string {
	s := string(b)
	if len(s) > n {
		return s[:n]
	}
	return s
}

// networkError wraps a transport-level error as providerUnavailable.
func networkError(err error) *model.ProviderError {
	return &model.ProviderError{Kind: model.KindProviderUnavailable, Message: err.Error()}
}
