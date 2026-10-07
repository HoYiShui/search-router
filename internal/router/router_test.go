package router

import (
	"context"
	"testing"
	"time"

	"search-router/internal/breaker"
	"search-router/internal/keypool"
	"search-router/internal/model"
)

type mockSearchFn func(ctx context.Context, req model.SearchRequest, key model.Key) ([]model.SearchResult, error)

type mockProvider struct {
	id          string
	contentType string
	caps        []string
	fn          mockSearchFn
}

func (m *mockProvider) ID() string          { return m.id }
func (m *mockProvider) ContentType() string { return m.contentType }
func (m *mockProvider) Capabilities() []string {
	return m.caps
}
func (m *mockProvider) Search(ctx context.Context, req model.SearchRequest, key model.Key) ([]model.SearchResult, error) {
	if m.fn != nil {
		return m.fn(ctx, req, key)
	}
	return []model.SearchResult{{Title: "ok", ContentType: m.contentType}}, nil
}

func k(id string, qps int) model.KeyConfig {
	return model.KeyConfig{ID: id, Enabled: true, QPS: qps}
}

func newMock(id, contentType string, caps []string, keys []model.KeyConfig, fn mockSearchFn) ProviderEntry {
	return ProviderEntry{
		Config:  model.ProviderConfig{ID: id, Enabled: true, Weight: 1, MaxKeyAttempts: 3, ContentType: contentType},
		Adapter: &mockProvider{id: id, contentType: contentType, caps: caps, fn: fn},
		Pool:    keypool.NewPool(keys, keypool.PoolOptions{}),
		Breaker: breaker.NewBreaker(breaker.BreakerOptions{}),
	}
}

func withinPct(got, want, pct int) bool {
	delta := want * pct / 100
	return got >= want-delta && got <= want+delta
}

func TestA1_SWRRWeightDistribution(t *testing.T) {
	p := newSWRRPicker()
	a := &ProviderEntry{Config: model.ProviderConfig{ID: "a", Weight: 1}}
	b := &ProviderEntry{Config: model.ProviderConfig{ID: "b", Weight: 3}}
	cands := []*ProviderEntry{a, b}

	counts := map[string]int{}
	const n = 10000
	for i := 0; i < n; i++ {
		c := p.next(cands)
		counts[c.Config.ID]++
	}
	if !withinPct(counts["a"], n/4, 5) {
		t.Errorf("a: got %d want ≈%d (±5%%)", counts["a"], n/4)
	}
	if !withinPct(counts["b"], 3*n/4, 5) {
		t.Errorf("b: got %d want ≈%d (±5%%)", counts["b"], 3*n/4)
	}
}

func TestA1_RouterSplitDistribution(t *testing.T) {
	entries := []ProviderEntry{
		newMock("a", model.ContentTypeAbstract, nil, []model.KeyConfig{k("a1", 100000)}, nil),
		newMock("b", model.ContentTypeAbstract, nil, []model.KeyConfig{k("b1", 100000)}, nil),
	}
	entries[0].Config.Weight = 1
	entries[1].Config.Weight = 3

	r := NewRouter(entries, model.ModeSplit)
	counts := map[string]int{}
	const n = 1200
	for i := 0; i < n; i++ {
		resp, err := r.Search(context.Background(), model.SearchRequest{Query: "q"})
		if err != nil {
			t.Fatalf("search %d: %v", i, err)
		}
		counts[resp.Meta.Provider]++
	}
	if !withinPct(counts["a"], n/4, 5) {
		t.Errorf("a: got %d want ≈%d (±5%%)", counts["a"], n/4)
	}
	if !withinPct(counts["b"], 3*n/4, 5) {
		t.Errorf("b: got %d want ≈%d (±5%%)", counts["b"], 3*n/4)
	}
}

func TestA2_SaturatedProviderSkipped(t *testing.T) {
	// provider "a" 空 pool（无可用 key）→ 无容量；"b" 正常。
	entries := []ProviderEntry{
		newMock("a", model.ContentTypeAbstract, nil, nil, nil),
		newMock("b", model.ContentTypeAbstract, nil, []model.KeyConfig{k("b1", 100000)}, nil),
	}
	r := NewRouter(entries, model.ModeSplit)

	resp, err := r.Search(context.Background(), model.SearchRequest{Query: "q"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if resp.Meta.Provider != "b" {
		t.Fatalf("provider: got %q want b", resp.Meta.Provider)
	}
	if len(resp.Meta.Attempts) != 1 {
		t.Fatalf("saturated provider 应被静默跳过，attempts: %+v", resp.Meta.Attempts)
	}
}

func TestA2_QuotaExhaustedProviderSkipped(t *testing.T) {
	// provider "a" 的 key 总配额=1：首次成功后耗尽，再次搜索应被跳过。
	quota := 1
	keyA := model.KeyConfig{ID: "a1", Enabled: true, QPS: 100000, TotalQuota: &quota}
	entryA := newMock("a", model.ContentTypeAbstract, nil, []model.KeyConfig{keyA}, nil)
	entryB := newMock("b", model.ContentTypeAbstract, nil, []model.KeyConfig{k("b1", 100000)}, nil)
	r := NewRouter([]ProviderEntry{entryA, entryB}, model.ModeSplit)

	resp1, err := r.Search(context.Background(), model.SearchRequest{Query: "q"})
	if err != nil {
		t.Fatalf("first search: %v", err)
	}
	if resp1.Meta.Provider != "a" {
		t.Fatalf("first pick: got %q want a (SWRR 应先选首个)", resp1.Meta.Provider)
	}

	// a 的 UsedTotal 已达配额 → 第二次搜索跳过 a，改走 b。
	resp2, err := r.Search(context.Background(), model.SearchRequest{Query: "q"})
	if err != nil {
		t.Fatalf("second search: %v", err)
	}
	if resp2.Meta.Provider != "b" {
		t.Fatalf("配额耗尽的 provider 应被跳过，got %q", resp2.Meta.Provider)
	}
}

func TestA3_ContentBodyFilter(t *testing.T) {
	entries := []ProviderEntry{
		newMock("abstractP", model.ContentTypeAbstract, nil, []model.KeyConfig{k("p1", 100000)}, nil),
		newMock("bodyP", model.ContentTypeBody, nil, []model.KeyConfig{k("p2", 100000)}, nil),
	}
	r := NewRouter(entries, model.ModeSplit)

	resp, err := r.Search(context.Background(), model.SearchRequest{Query: "q", Content: model.ContentBody})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if resp.Meta.Provider != "bodyP" {
		t.Fatalf("content=body 应排除 abstract provider，got %q", resp.Meta.Provider)
	}
}

func TestA4_KeyFailureRetriesWithinProvider(t *testing.T) {
	fn := func(_ context.Context, _ model.SearchRequest, key model.Key) ([]model.SearchResult, error) {
		switch key.ID {
		case "k1":
			return nil, &model.ProviderError{Kind: model.KindKeyInvalid}
		case "k2":
			return nil, &model.ProviderError{Kind: model.KindKeyRateLimited}
		default:
			return []model.SearchResult{{Title: "ok"}}, nil
		}
	}
	entry := newMock("p", model.ContentTypeAbstract, nil,
		[]model.KeyConfig{k("k1", 100000), k("k2", 100000), k("k3", 100000)}, fn)
	entry.Config.MaxKeyAttempts = 3
	r := NewRouter([]ProviderEntry{entry}, model.ModeSplit)

	resp, err := r.Search(context.Background(), model.SearchRequest{Query: "q"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(resp.Meta.Attempts) != 3 {
		t.Fatalf("attempts: got %d want 3 (%+v)", len(resp.Meta.Attempts), resp.Meta.Attempts)
	}
	if resp.Meta.Attempts[0].KeyID != "k1" || resp.Meta.Attempts[1].KeyID != "k2" || resp.Meta.Attempts[2].KeyID != "k3" {
		t.Fatalf("key 顺序: %+v", resp.Meta.Attempts)
	}
	if !resp.Meta.Attempts[2].OK {
		t.Fatalf("第三次应成功: %+v", resp.Meta.Attempts[2])
	}
	// key 级故障不记熔断
	if !entry.Breaker.Allow() {
		t.Fatal("key 级故障不应熔断 provider")
	}
}

func TestA4_MaxKeyAttemptsUpperBound(t *testing.T) {
	fn := func(_ context.Context, _ model.SearchRequest, _ model.Key) ([]model.SearchResult, error) {
		return nil, &model.ProviderError{Kind: model.KindKeyInvalid}
	}
	entry := newMock("p", model.ContentTypeAbstract, nil,
		[]model.KeyConfig{k("k1", 100000), k("k2", 100000), k("k3", 100000)}, fn)
	entry.Config.MaxKeyAttempts = 2
	r := NewRouter([]ProviderEntry{entry}, model.ModeSplit)

	_, err := r.Search(context.Background(), model.SearchRequest{Query: "q"})
	apf, ok := err.(*model.AllProvidersFailedError)
	if !ok {
		t.Fatalf("err type: got %T want *AllProvidersFailedError", err)
	}
	if len(apf.Attempts) != 2 {
		t.Fatalf("尝试 key 数: got %d want ≤2 (MaxKeyAttempts=2)", len(apf.Attempts))
	}
	if !entry.Breaker.Allow() {
		t.Fatal("key 级故障耗尽不应熔断 provider")
	}
}

func TestA5_Degradation_SwitchedFrom(t *testing.T) {
	fnA := func(_ context.Context, _ model.SearchRequest, _ model.Key) ([]model.SearchResult, error) {
		return nil, &model.ProviderError{Kind: model.KindProviderUnavailable}
	}
	entries := []ProviderEntry{
		newMock("a", model.ContentTypeAbstract, nil, []model.KeyConfig{k("a1", 100000)}, fnA),
		newMock("b", model.ContentTypeAbstract, nil, []model.KeyConfig{k("b1", 100000)}, nil),
	}
	r := NewRouter(entries, model.ModeSplit)

	resp, err := r.Search(context.Background(), model.SearchRequest{Query: "q"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if resp.Meta.Provider != "b" {
		t.Fatalf("provider: got %q want b", resp.Meta.Provider)
	}
	if !resp.Meta.Degraded {
		t.Fatal("跨 provider 降级后 Degraded 应为 true")
	}
	if resp.Meta.SwitchedFrom != "a" {
		t.Fatalf("switchedFrom: got %q want a", resp.Meta.SwitchedFrom)
	}
	if len(resp.Meta.Attempts) != 2 {
		t.Fatalf("attempts: got %d want 2", len(resp.Meta.Attempts))
	}
	if resp.Meta.Attempts[0].Code != string(model.KindProviderUnavailable) {
		t.Fatalf("first attempt code: got %q", resp.Meta.Attempts[0].Code)
	}
}

func TestA5_BadRequest_NoRetry(t *testing.T) {
	fn := func(_ context.Context, _ model.SearchRequest, _ model.Key) ([]model.SearchResult, error) {
		return nil, &model.ProviderError{Kind: model.KindBadRequest}
	}
	entries := []ProviderEntry{
		newMock("a", model.ContentTypeAbstract, nil, []model.KeyConfig{k("a1", 100000)}, fn),
		newMock("b", model.ContentTypeAbstract, nil, []model.KeyConfig{k("b1", 100000)}, nil),
	}
	r := NewRouter(entries, model.ModeSplit)

	_, err := r.Search(context.Background(), model.SearchRequest{Query: "q"})
	perr, ok := err.(*model.ProviderError)
	if !ok || perr.Kind != model.KindBadRequest {
		t.Fatalf("err: got %v want badRequest", err)
	}
}

func TestA5_CircuitOpen_Recorded(t *testing.T) {
	entries := []ProviderEntry{
		newMock("a", model.ContentTypeAbstract, nil, []model.KeyConfig{k("a1", 100000)}, nil),
		newMock("b", model.ContentTypeAbstract, nil, []model.KeyConfig{k("b1", 100000)}, nil),
	}
	// 打开 a 的熔断
	for i := 0; i < 3; i++ {
		entries[0].Breaker.RecordFailure()
	}
	r := NewRouter(entries, model.ModeSplit)

	resp, err := r.Search(context.Background(), model.SearchRequest{Query: "q"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if resp.Meta.Provider != "b" {
		t.Fatalf("provider: got %q want b", resp.Meta.Provider)
	}
	if len(resp.Meta.Attempts) < 2 || resp.Meta.Attempts[0].Code != "circuit_open" {
		t.Fatalf("应记录 circuit_open attempt: %+v", resp.Meta.Attempts)
	}
}

func TestA5_MetaConstruction(t *testing.T) {
	// provider 只支持 timeRange；请求带满软参数。
	entry := newMock("a", model.ContentTypeBody, []string{"timeRange"},
		[]model.KeyConfig{k("a1", 100000)}, nil)
	r := NewRouter([]ProviderEntry{entry}, model.ModeSplit)

	resp, err := r.Search(context.Background(), model.SearchRequest{
		Query:      "q",
		Site:       "x.com",
		TimeRange:  "w",
		Country:    "us",
		Lang:       "en",
		Page:       3,
		SafeSearch: model.SafeSearchStrict,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	wantIgnored := []string{"site", "country", "lang", "page", "safeSearch"}
	if len(resp.Meta.IgnoredParams) != len(wantIgnored) {
		t.Fatalf("ignoredParams: got %v want %v", resp.Meta.IgnoredParams, wantIgnored)
	}
	for i, w := range wantIgnored {
		if resp.Meta.IgnoredParams[i] != w {
			t.Fatalf("ignoredParams[%d]: got %q want %q", i, resp.Meta.IgnoredParams[i], w)
		}
	}
	if resp.Meta.Degraded {
		t.Fatal("单次成功不应 degraded")
	}
	if resp.Meta.SwitchedFrom != "" {
		t.Fatalf("switchedFrom: got %q want empty", resp.Meta.SwitchedFrom)
	}
	if len(resp.Meta.Attempts) != 1 || !resp.Meta.Attempts[0].OK {
		t.Fatalf("attempts: %+v", resp.Meta.Attempts)
	}
}

func TestTimeout_TreatedAsProviderUnavailable(t *testing.T) {
	fn := func(ctx context.Context, _ model.SearchRequest, _ model.Key) ([]model.SearchResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	entry := newMock("p", model.ContentTypeAbstract, nil, []model.KeyConfig{k("p1", 100000)}, fn)
	entry.Config.Timeout = 20 * time.Millisecond
	r := NewRouter([]ProviderEntry{entry}, model.ModeSplit)

	_, err := r.Search(context.Background(), model.SearchRequest{Query: "q"})
	apf, ok := err.(*model.AllProvidersFailedError)
	if !ok {
		t.Fatalf("err type: got %T want *AllProvidersFailedError", err)
	}
	if len(apf.Attempts) == 0 || apf.Attempts[0].Code != string(model.KindProviderUnavailable) {
		t.Fatalf("超时应归类 providerUnavailable: %+v", apf.Attempts)
	}
}
