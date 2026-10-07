// Package breaker implements a provider-level circuit breaker.
//
// State machine: closed → open → half-open → closed. Consecutive failures
// (RecordFailure) reaching the threshold open the circuit; after the cooldown
// elapses, Allow() admits a single probe (half-open) whose outcome decides
// whether to close or re-open.
package breaker

import (
	"sync"
	"time"

	"search-router/internal/model"
)

// now is the package clock; tests in this package may override it.
var now = time.Now

// BreakerOptions holds circuit-breaker parameters with sane defaults.
type BreakerOptions struct {
	Threshold int           // 连续失败达此值即 open
	Cooldown  time.Duration // open 冷却时长，期满进 half-open
}

type Breaker struct {
	mu        sync.Mutex
	threshold int
	cooldown  time.Duration

	state         model.BreakerState
	failureCount  int
	openedAt      time.Time
	cooldownUntil time.Time
}

func NewBreaker(opts BreakerOptions) *Breaker {
	if opts.Threshold <= 0 {
		opts.Threshold = 3
	}
	if opts.Cooldown <= 0 {
		opts.Cooldown = 5 * time.Minute
	}
	return &Breaker{
		threshold: opts.Threshold,
		cooldown:  opts.Cooldown,
		state:     model.BreakerClosed,
	}
}

// Allow reports whether a request may proceed. open 期间返回 false。
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state == model.BreakerOpen && !now().Before(b.cooldownUntil) {
		// 冷却期满 → half-open，放行一次探测
		b.state = model.BreakerHalfOpen
	}
	return b.state != model.BreakerOpen
}

// RecordFailure counts a failure; consecutive failures ≥ threshold open the
// circuit, and a failed half-open probe re-opens it immediately.
func (b *Breaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case model.BreakerClosed:
		b.failureCount++
		if b.failureCount >= b.threshold {
			b.state = model.BreakerOpen
			b.openedAt = now()
			b.cooldownUntil = now().Add(b.cooldown)
			b.failureCount = 0
		}
	case model.BreakerHalfOpen:
		b.state = model.BreakerOpen
		b.openedAt = now()
		b.cooldownUntil = now().Add(b.cooldown)
		b.failureCount = 0
	case model.BreakerOpen:
		// 已在 open，忽略
	}
}

// RecordSuccess closes the circuit and resets the consecutive-failure count.
func (b *Breaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.state = model.BreakerClosed
	b.failureCount = 0
}
