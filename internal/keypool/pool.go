// Package keypool manages a single provider's keys: round-robin selection,
// per-key token bucket (QPS), daily/monthly/total quotas, and the key state
// machine (active / cooling / quarantined). All shared state is guarded by a
// single sync.Mutex; quota reset is lazy (checked on Acquire/HasCapacity).
package keypool

import (
	"errors"
	"sync"
	"time"

	"search-router/internal/model"
)

// now is the package clock; tests in this package may override it.
var now = time.Now

// ErrNoKeyAvailable is returned by Acquire when no key can serve a request.
var ErrNoKeyAvailable = errors.New("no key available")

// PoolOptions holds key-pool global parameters with sane defaults.
type PoolOptions struct {
	RateLimitCooldown time.Duration // 429 无 Retry-After 时的默认冷却
}

// qreason records why a key is quarantined, which determines when (if ever)
// it recovers to active.
type qreason int

const (
	qrNone      qreason = iota
	qrPermanent         // keyInvalid / 总配额：永久，仅重启清态
	qrDaily             // 日配额：到 UTC 日边界回 active
	qrMonthly           // 月配额（或上游被动配额耗尽）：到 UTC 月边界回 active
)

type keyEntry struct {
	cfg     model.KeyConfig
	rt      model.KeyRuntime
	qreason qreason
}

type Pool struct {
	mu                sync.Mutex
	keys              []*keyEntry
	rr                int // 轮询游标
	rateLimitCooldown time.Duration
}

func NewPool(keys []model.KeyConfig, opts PoolOptions) *Pool {
	if opts.RateLimitCooldown <= 0 {
		opts.RateLimitCooldown = time.Second
	}
	p := &Pool{rateLimitCooldown: opts.RateLimitCooldown}
	for _, kc := range keys {
		if !kc.Enabled {
			continue
		}
		t := now()
		p.keys = append(p.keys, &keyEntry{
			cfg: kc,
			rt: model.KeyRuntime{
				ID:           kc.ID,
				State:        model.KeyActive,
				Tokens:       float64(kc.QPS), // 桶容量 = QPS，起始满
				LastRefillAt: t,
				DayStamp:     utcDay(t),
				MonthStamp:   utcMonth(t),
			},
		})
	}
	return p
}

func utcDay(t time.Time) string   { return t.UTC().Format("2006-01-02") }
func utcMonth(t time.Time) string { return t.UTC().Format("2006-01") }

// HasCapacity reports whether at least one key can currently serve a request.
func (p *Pool) HasCapacity() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.refreshLocked()
	for _, k := range p.keys {
		if k.rt.State != model.KeyActive {
			continue
		}
		p.refill(k)
		if k.rt.Tokens >= 1 {
			return true
		}
	}
	return false
}

// Acquire returns the next available key in round-robin order, consuming one
// token. It returns ErrNoKeyAvailable when no key can serve right now.
func (p *Pool) Acquire() (model.Key, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.refreshLocked()
	n := len(p.keys)
	if n == 0 {
		return model.Key{}, ErrNoKeyAvailable
	}

	for i := 0; i < n; i++ {
		idx := (p.rr + i) % n
		k := p.keys[idx]
		if k.rt.State != model.KeyActive {
			continue
		}
		p.refill(k)
		if k.rt.Tokens >= 1 {
			k.rt.Tokens--
			p.rr = (idx + 1) % n
			return model.Key{ID: k.cfg.ID, Label: k.cfg.Label, Value: k.cfg.Value}, nil
		}
	}
	return model.Key{}, ErrNoKeyAvailable
}

// ReportSuccess records a successful request: bump usage counters and clear
// the last error.
func (p *Pool) ReportSuccess(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	k := p.find(id)
	if k == nil {
		return
	}
	k.rt.UsedToday++
	k.rt.UsedMonth++
	k.rt.UsedTotal++
	k.rt.LastError = ""
	k.rt.LastErrorKind = ""
}

// ReportFailure records a failed request, transitioning the key state
// according to err.Kind. Non-key faults (providerUnavailable / badRequest)
// leave the key state unchanged.
func (p *Pool) ReportFailure(id string, err *model.ProviderError) {
	p.mu.Lock()
	defer p.mu.Unlock()

	k := p.find(id)
	if k == nil || err == nil {
		return
	}
	k.rt.LastError = err.Message
	k.rt.LastErrorKind = err.Kind

	switch err.Kind {
	case model.KindKeyRateLimited:
		cooldown := p.rateLimitCooldown
		if err.RetryAfter > 0 {
			cooldown = err.RetryAfter
		}
		k.rt.State = model.KeyCooling
		k.rt.CooldownUntil = now().Add(cooldown)
	case model.KindKeyInvalid:
		k.rt.State = model.KeyQuarantined
		k.qreason = qrPermanent
	case model.KindKeyQuotaExhausted:
		k.rt.State = model.KeyQuarantined
		k.qreason = qrMonthly // 上游被动配额耗尽：隔离到下一 reset 边界
	}
}

func (p *Pool) find(id string) *keyEntry {
	for _, k := range p.keys {
		if k.cfg.ID == id {
			return k
		}
	}
	return nil
}

// refreshLocked lazily resets quota counters at UTC day/month boundaries,
// expires cooling, recovers quarantined keys whose boundary has passed, and
// proactively quarantines keys that have exhausted a configured quota.
func (p *Pool) refreshLocked() {
	t := now()
	day := utcDay(t)
	month := utcMonth(t)

	for _, k := range p.keys {
		rt := &k.rt
		dayChanged := rt.DayStamp != day
		monthChanged := rt.MonthStamp != month

		if dayChanged {
			rt.UsedToday = 0
			rt.DayStamp = day
		}
		if monthChanged {
			rt.UsedMonth = 0
			rt.MonthStamp = month
		}

		switch rt.State {
		case model.KeyCooling:
			if !t.Before(rt.CooldownUntil) {
				rt.State = model.KeyActive
				rt.CooldownUntil = time.Time{}
			}
		case model.KeyQuarantined:
			recovered := false
			switch k.qreason {
			case qrDaily:
				recovered = dayChanged
			case qrMonthly:
				recovered = monthChanged
			}
			if recovered {
				rt.State = model.KeyActive
				k.qreason = qrNone
			}
		}

		// 主动配额判（仅 active）
		if rt.State == model.KeyActive {
			switch {
			case k.cfg.TotalQuota != nil && rt.UsedTotal >= *k.cfg.TotalQuota:
				rt.State = model.KeyQuarantined
				k.qreason = qrPermanent
				rt.LastErrorKind = model.KindKeyQuotaExhausted
			case k.cfg.DailyQuota != nil && rt.UsedToday >= *k.cfg.DailyQuota:
				rt.State = model.KeyQuarantined
				k.qreason = qrDaily
				rt.LastErrorKind = model.KindKeyQuotaExhausted
			case k.cfg.MonthlyQuota != nil && rt.UsedMonth >= *k.cfg.MonthlyQuota:
				rt.State = model.KeyQuarantined
				k.qreason = qrMonthly
				rt.LastErrorKind = model.KindKeyQuotaExhausted
			}
		}
	}
}

// refill adds tokens elapsed since the last refill, capped at the bucket
// capacity (= QPS).
func (p *Pool) refill(k *keyEntry) {
	rt := &k.rt
	qps := float64(k.cfg.QPS)
	elapsed := now().Sub(rt.LastRefillAt).Seconds()
	if elapsed > 0 {
		rt.Tokens += elapsed * qps
		if rt.Tokens > qps {
			rt.Tokens = qps
		}
		rt.LastRefillAt = now()
	}
}
