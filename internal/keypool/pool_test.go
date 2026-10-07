package keypool

import (
	"testing"
	"time"

	"search-router/internal/model"
)

func useFakeClock(t *testing.T, cur *time.Time) {
	t.Helper()
	now = func() time.Time { return *cur }
	t.Cleanup(func() { now = time.Now })
}

func intPtr(v int) *int { return &v }

func oneKey(id string, qps int, mutate func(*model.KeyConfig)) model.KeyConfig {
	kc := model.KeyConfig{ID: id, Enabled: true, QPS: qps}
	if mutate != nil {
		mutate(&kc)
	}
	return kc
}

func TestRoundRobin_Order(t *testing.T) {
	cur := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	useFakeClock(t, &cur)

	p := NewPool([]model.KeyConfig{
		oneKey("k1", 100, nil),
		oneKey("k2", 100, nil),
	}, PoolOptions{})

	want := []string{"k1", "k2", "k1", "k2"}
	for i, w := range want {
		k, err := p.Acquire()
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		if k.ID != w {
			t.Fatalf("acquire %d: got %q want %q", i, k.ID, w)
		}
	}
}

func TestDisabledKeys_Skipped(t *testing.T) {
	cur := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	useFakeClock(t, &cur)

	disabled := oneKey("k1", 100, nil)
	disabled.Enabled = false
	p := NewPool([]model.KeyConfig{disabled, oneKey("k2", 100, nil)}, PoolOptions{})

	k, err := p.Acquire()
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if k.ID != "k2" {
		t.Fatalf("got %q want k2 (disabled k1 must be skipped)", k.ID)
	}
}

func TestTokenBucket_Refill(t *testing.T) {
	cur := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	useFakeClock(t, &cur)

	p := NewPool([]model.KeyConfig{oneKey("k1", 5, nil)}, PoolOptions{})

	for i := 0; i < 5; i++ {
		if _, err := p.Acquire(); err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
	}
	if _, err := p.Acquire(); err != ErrNoKeyAvailable {
		t.Fatalf("after draining tokens: got %v want ErrNoKeyAvailable", err)
	}

	cur = cur.Add(time.Second) // 1s → 补 5 个令牌
	if _, err := p.Acquire(); err != nil {
		t.Fatalf("after 1s refill: %v", err)
	}
}

func TestA2_HasCapacity_Saturated(t *testing.T) {
	cur := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	useFakeClock(t, &cur)

	p := NewPool([]model.KeyConfig{oneKey("k1", 1, nil)}, PoolOptions{})

	if !p.HasCapacity() {
		t.Fatal("initial: HasCapacity should be true")
	}
	if _, err := p.Acquire(); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if p.HasCapacity() {
		t.Fatal("after draining the only token: HasCapacity should be false")
	}
}

func TestA7_KeyStateMachine_429Cooling(t *testing.T) {
	cur := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	useFakeClock(t, &cur)

	p := NewPool([]model.KeyConfig{oneKey("k1", 100, nil)}, PoolOptions{})

	k, err := p.Acquire()
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	p.ReportFailure(k.ID, &model.ProviderError{Kind: model.KindKeyRateLimited, RetryAfter: 2 * time.Second})

	if p.HasCapacity() {
		t.Fatal("cooling 期间 HasCapacity 应为 false")
	}
	if _, err := p.Acquire(); err != ErrNoKeyAvailable {
		t.Fatalf("cooling 期间 Acquire: got %v want ErrNoKeyAvailable", err)
	}

	cur = cur.Add(2 * time.Second) // 冷却到期
	if !p.HasCapacity() {
		t.Fatal("冷却到期后 HasCapacity 应为 true")
	}
	if _, err := p.Acquire(); err != nil {
		t.Fatalf("冷却到期后 Acquire: %v", err)
	}
}

func TestA7_KeyStateMachine_InvalidPermanent(t *testing.T) {
	cur := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	useFakeClock(t, &cur)

	p := NewPool([]model.KeyConfig{oneKey("k1", 100, nil)}, PoolOptions{})

	k, _ := p.Acquire()
	p.ReportFailure(k.ID, &model.ProviderError{Kind: model.KindKeyInvalid})

	if p.HasCapacity() {
		t.Fatal("quarantined 期间 HasCapacity 应为 false")
	}

	cur = cur.Add(30 * 24 * time.Hour) // 跨月也回不来
	if p.HasCapacity() {
		t.Fatal("keyInvalid 永久隔离，跨月也不应恢复")
	}
	if _, err := p.Acquire(); err != ErrNoKeyAvailable {
		t.Fatalf("keyInvalid 永久隔离 Acquire: got %v want ErrNoKeyAvailable", err)
	}
}

func TestA8_Quota_DailyReset(t *testing.T) {
	cur := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	useFakeClock(t, &cur)

	p := NewPool([]model.KeyConfig{
		oneKey("k1", 100, func(k *model.KeyConfig) { k.DailyQuota = intPtr(2) }),
	}, PoolOptions{})

	for i := 0; i < 2; i++ {
		k, err := p.Acquire()
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		p.ReportSuccess(k.ID)
	}

	if _, err := p.Acquire(); err != ErrNoKeyAvailable {
		t.Fatalf("日配额耗尽后 Acquire: got %v want ErrNoKeyAvailable", err)
	}

	cur = cur.Add(24 * time.Hour) // 跨 UTC 日边界
	if _, err := p.Acquire(); err != nil {
		t.Fatalf("跨日后应回 active: %v", err)
	}
}

func TestA8_Quota_MonthlyReset(t *testing.T) {
	cur := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	useFakeClock(t, &cur)

	p := NewPool([]model.KeyConfig{
		oneKey("k1", 100, func(k *model.KeyConfig) { k.MonthlyQuota = intPtr(2) }),
	}, PoolOptions{})

	for i := 0; i < 2; i++ {
		k, err := p.Acquire()
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		p.ReportSuccess(k.ID)
	}

	if _, err := p.Acquire(); err != ErrNoKeyAvailable {
		t.Fatalf("月配额耗尽后 Acquire: got %v want ErrNoKeyAvailable", err)
	}

	cur = time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC) // 跨 UTC 月边界
	if _, err := p.Acquire(); err != nil {
		t.Fatalf("跨月后应回 active: %v", err)
	}
}

func TestA8_Quota_TotalPermanent(t *testing.T) {
	cur := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	useFakeClock(t, &cur)

	p := NewPool([]model.KeyConfig{
		oneKey("k1", 100, func(k *model.KeyConfig) { k.TotalQuota = intPtr(2) }),
	}, PoolOptions{})

	for i := 0; i < 2; i++ {
		k, err := p.Acquire()
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		p.ReportSuccess(k.ID)
	}

	if _, err := p.Acquire(); err != ErrNoKeyAvailable {
		t.Fatalf("总配额耗尽后 Acquire: got %v want ErrNoKeyAvailable", err)
	}

	cur = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC) // 跨日+跨月
	if _, err := p.Acquire(); err != ErrNoKeyAvailable {
		t.Fatalf("总配额永久隔离：got %v want ErrNoKeyAvailable", err)
	}
}

func TestA8_Quota_PassiveExhausted(t *testing.T) {
	cur := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	useFakeClock(t, &cur)

	p := NewPool([]model.KeyConfig{oneKey("k1", 100, nil)}, PoolOptions{})

	k, _ := p.Acquire()
	p.ReportFailure(k.ID, &model.ProviderError{Kind: model.KindKeyQuotaExhausted})

	if p.HasCapacity() {
		t.Fatal("被动配额耗尽后 HasCapacity 应为 false")
	}

	cur = time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC) // 跨月边界
	if _, err := p.Acquire(); err != nil {
		t.Fatalf("被动配额耗尽到月边界应回 active: %v", err)
	}
}

func TestA8_Quota_PassiveExhausted_TotalPermanent(t *testing.T) {
	cur := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	useFakeClock(t, &cur)

	p := NewPool([]model.KeyConfig{
		oneKey("k1", 100, func(k *model.KeyConfig) { k.TotalQuota = intPtr(100) }),
	}, PoolOptions{})

	k, _ := p.Acquire()
	p.ReportFailure(k.ID, &model.ProviderError{Kind: model.KindKeyQuotaExhausted})

	// 配置了 TotalQuota → 上游被动耗尽视为永久，跨月也不回 active。
	cur = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if _, err := p.Acquire(); err != ErrNoKeyAvailable {
		t.Fatalf("配置 TotalQuota 的被动耗尽应永久隔离，got %v", err)
	}
}
