// Package router is the core differentiation: cross-provider proactive routing.
// It filters eligible providers (enabled + circuit closed + has capacity +
// hard-capability satisfied), picks one by smooth weighted round-robin (split)
// or priority (failover), then retries within the provider's key pool bounded
// by MaxKeyAttempts before moving to the next provider.
package router

import (
	"context"
	"sort"
	"time"

	"search-router/internal/breaker"
	"search-router/internal/keypool"
	"search-router/internal/model"
	"search-router/internal/providers"
)

type ProviderEntry struct {
	Config  model.ProviderConfig // weight / priority / contentType / timeout / maxKeyAttempts…
	Adapter providers.Provider
	Pool    *keypool.Pool
	Breaker *breaker.Breaker
}

type Router struct {
	entries []ProviderEntry
	mode    string
	picker  *swrrPicker
}

func NewRouter(entries []ProviderEntry, mode string) *Router {
	return &Router{entries: entries, mode: mode, picker: newSWRRPicker()}
}

// Search runs the full "select provider → acquire key → retry" loop, returning
// an *model.AllProvidersFailedError when every provider fails, or an
// *model.ProviderError{Kind: badRequest} when a provider rejects the request.
func (r *Router) Search(ctx context.Context, req model.SearchRequest) (*model.SearchResponse, error) {
	start := time.Now()
	var attempts []model.Attempt

	// 静态过滤：启用 + 硬能力
	entries := r.staticCandidates(req)
	if r.mode == model.ModeFailover {
		sort.SliceStable(entries, func(i, j int) bool {
			return entries[i].Config.Priority < entries[j].Config.Priority
		})
	}

	tried := make(map[string]bool)

	for {
		// 动态过滤：熔断未开 + HasCapacity
		eligible := make([]*ProviderEntry, 0, len(entries))
		for _, e := range entries {
			if tried[e.Config.ID] {
				continue
			}
			if !e.Breaker.Allow() {
				attempts = append(attempts, model.Attempt{
					Provider: e.Adapter.ID(),
					OK:       false,
					Code:     "circuit_open",
					Message:  "circuit open",
				})
				tried[e.Config.ID] = true
				continue
			}
			if !e.Pool.HasCapacity() {
				tried[e.Config.ID] = true
				continue
			}
			eligible = append(eligible, e)
		}

		if len(eligible) == 0 {
			break
		}

		var entry *ProviderEntry
		if r.mode == model.ModeFailover {
			entry = eligible[0]
		} else {
			entry = r.picker.next(eligible)
		}
		tried[entry.Config.ID] = true

		results, done, abortErr := r.tryProvider(ctx, req, entry, &attempts)
		if abortErr != nil {
			return nil, abortErr
		}
		if done {
			return r.buildResponse(entry, results, req, attempts, start), nil
		}
	}

	return nil, &model.AllProvidersFailedError{Attempts: attempts}
}

// staticCandidates filters by enabled + hard capability (content=body excludes
// abstract providers).
func (r *Router) staticCandidates(req model.SearchRequest) []*ProviderEntry {
	var out []*ProviderEntry
	for i := range r.entries {
		e := &r.entries[i]
		if !e.Config.Enabled {
			continue
		}
		if req.Content == model.ContentBody && e.Adapter.ContentType() == model.ContentTypeAbstract {
			continue
		}
		out = append(out, e)
	}
	return out
}

// tryProvider attempts up to MaxKeyAttempts keys within a single provider.
// It returns (results, done, abortErr): done=true on success; abortErr non-nil
// for a badRequest that must not be retried; otherwise it reports failure so
// the caller moves to the next provider.
func (r *Router) tryProvider(ctx context.Context, req model.SearchRequest, entry *ProviderEntry, attempts *[]model.Attempt) ([]model.SearchResult, bool, error) {
	maxAttempts := entry.Config.MaxKeyAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}

	for i := 0; i < maxAttempts; i++ {
		key, err := entry.Pool.Acquire()
		if err != nil {
			*attempts = append(*attempts, model.Attempt{
				Provider: entry.Adapter.ID(),
				OK:       false,
				Code:     "no_key_available",
				Message:  err.Error(),
			})
			break
		}

		attemptStart := time.Now()

		searchCtx := ctx
		var cancel context.CancelFunc
		if entry.Config.Timeout > 0 {
			searchCtx, cancel = context.WithTimeout(ctx, entry.Config.Timeout)
		}

		results, searchErr := entry.Adapter.Search(searchCtx, req, key)
		if cancel != nil {
			cancel()
		}
		took := time.Since(attemptStart).Milliseconds()

		if searchErr == nil {
			entry.Pool.ReportSuccess(key.ID)
			entry.Breaker.RecordSuccess()
			*attempts = append(*attempts, model.Attempt{
				Provider: entry.Adapter.ID(),
				KeyID:    key.ID,
				OK:       true,
				TookMs:   took,
			})
			return results, true, nil
		}

		perr := asProviderError(searchErr)
		switch perr.Kind {
		case model.KindKeyInvalid, model.KindKeyRateLimited, model.KindKeyQuotaExhausted:
			// key 故障：换 key，不记熔断
			entry.Pool.ReportFailure(key.ID, perr)
			*attempts = append(*attempts, model.Attempt{
				Provider: entry.Adapter.ID(),
				KeyID:    key.ID,
				OK:       false,
				Code:     string(perr.Kind),
				Message:  perr.Message,
				TookMs:   took,
			})
		case model.KindProviderUnavailable:
			// provider 故障：记熔断，换 provider
			entry.Breaker.RecordFailure()
			*attempts = append(*attempts, model.Attempt{
				Provider: entry.Adapter.ID(),
				KeyID:    key.ID,
				OK:       false,
				Code:     string(perr.Kind),
				Message:  perr.Message,
				TookMs:   took,
			})
			return nil, false, nil
		case model.KindBadRequest:
			// 参数错：不重试，直接返回
			*attempts = append(*attempts, model.Attempt{
				Provider: entry.Adapter.ID(),
				KeyID:    key.ID,
				OK:       false,
				Code:     string(perr.Kind),
				Message:  perr.Message,
				TookMs:   took,
			})
			return nil, false, perr
		}
	}

	// 试满 MaxKeyAttempts 个 key 仍未成功 → 换 provider
	return nil, false, nil
}

// buildResponse constructs the Meta from the winning entry and the recorded
// attempts.
func (r *Router) buildResponse(entry *ProviderEntry, results []model.SearchResult, req model.SearchRequest, attempts []model.Attempt, start time.Time) *model.SearchResponse {
	last := attempts[len(attempts)-1]

	switchedFrom := ""
	if len(attempts) >= 2 && attempts[0].Provider != last.Provider {
		switchedFrom = attempts[0].Provider
	}

	meta := model.Meta{
		Provider:      last.Provider,
		KeyID:         last.KeyID,
		TookMs:        time.Since(start).Milliseconds(),
		Degraded:      len(attempts) > 1,
		SwitchedFrom:  switchedFrom,
		IgnoredParams: ignoredParamsForProvider(entry.Adapter.Capabilities(), req),
		Attempts:      attempts,
	}
	return &model.SearchResponse{Results: results, Meta: meta}
}

func asProviderError(err error) *model.ProviderError {
	if perr, ok := err.(*model.ProviderError); ok {
		return perr
	}
	return &model.ProviderError{Kind: model.KindProviderUnavailable, Message: err.Error()}
}

// hasCap reports whether c is in caps.
func hasCap(caps []string, c string) bool {
	for _, x := range caps {
		if x == c {
			return true
		}
	}
	return false
}

// ignoredParamsForProvider lists the soft params the request set but the
// provider does not support (and which were therefore silently dropped).
func ignoredParamsForProvider(caps []string, req model.SearchRequest) []string {
	var out []string
	if req.Site != "" && !hasCap(caps, "site") {
		out = append(out, "site")
	}
	if req.TimeRange != "" && !hasCap(caps, "timeRange") {
		out = append(out, "timeRange")
	}
	if req.Country != "" && !hasCap(caps, "country") {
		out = append(out, "country")
	}
	if req.Lang != "" && !hasCap(caps, "lang") {
		out = append(out, "lang")
	}
	if req.Page > 1 && !hasCap(caps, "page") {
		out = append(out, "page")
	}
	if req.SafeSearch != "" && req.SafeSearch != model.SafeSearchOff && !hasCap(caps, "safeSearch") {
		out = append(out, "safeSearch")
	}
	return out
}
