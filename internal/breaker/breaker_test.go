package breaker

import (
	"testing"
	"time"

	"search-router/internal/model"
)

// fakeClock 返回一个可控时钟：测试里改 *cur 即推进时间。
func fakeClock(t *testing.T, cur *time.Time) {
	t.Helper()
	now = func() time.Time { return *cur }
	t.Cleanup(func() { now = time.Now })
}

func TestA6_ClosedToOpenToHalfOpenToClosed(t *testing.T) {
	cur := time.Unix(0, 0)
	fakeClock(t, &cur)

	b := NewBreaker(BreakerOptions{Threshold: 3, Cooldown: 5 * time.Minute})

	if b.state != model.BreakerClosed {
		t.Fatalf("initial state: got %v want closed", b.state)
	}

	// 连续失败未达阈值仍 closed
	b.RecordFailure()
	b.RecordFailure()
	if b.state != model.BreakerClosed {
		t.Fatalf("after 2 failures: got %v want closed", b.state)
	}
	if !b.Allow() {
		t.Fatal("closed 期间 Allow 应为 true")
	}

	// 第 3 次失败 → open
	b.RecordFailure()
	if b.state != model.BreakerOpen {
		t.Fatalf("after 3 failures: got %v want open", b.state)
	}
	if b.Allow() {
		t.Fatal("open 期间 Allow 应为 false")
	}

	// 冷却期满 → half-open（放行探测）
	cur = cur.Add(5*time.Minute + time.Second)
	if !b.Allow() {
		t.Fatal("冷却期满应放行探测")
	}
	if b.state != model.BreakerHalfOpen {
		t.Fatalf("after cooldown: got %v want half-open", b.state)
	}

	// 探测成功 → closed
	b.RecordSuccess()
	if b.state != model.BreakerClosed {
		t.Fatalf("after probe success: got %v want closed", b.state)
	}
	if !b.Allow() {
		t.Fatal("closed 后 Allow 应为 true")
	}
}

func TestA6_HalfOpenToOpen(t *testing.T) {
	cur := time.Unix(0, 0)
	fakeClock(t, &cur)

	b := NewBreaker(BreakerOptions{Threshold: 3, Cooldown: 5 * time.Minute})

	// 达到 open
	b.RecordFailure()
	b.RecordFailure()
	b.RecordFailure()
	if b.state != model.BreakerOpen {
		t.Fatalf("state: got %v want open", b.state)
	}

	// 冷却期满进 half-open
	cur = cur.Add(5*time.Minute + time.Second)
	if !b.Allow() {
		t.Fatal("冷却期满应放行探测")
	}
	if b.state != model.BreakerHalfOpen {
		t.Fatalf("state: got %v want half-open", b.state)
	}

	// 探测失败 → 立即回 open
	b.RecordFailure()
	if b.state != model.BreakerOpen {
		t.Fatalf("after probe failure: got %v want open", b.state)
	}
	if b.Allow() {
		t.Fatal("重新 open 后 Allow 应为 false")
	}
}

func TestA6_AllowDuringOpen(t *testing.T) {
	cur := time.Unix(0, 0)
	fakeClock(t, &cur)

	b := NewBreaker(BreakerOptions{Threshold: 3, Cooldown: 5 * time.Minute})
	b.RecordFailure()
	b.RecordFailure()
	b.RecordFailure()

	// open 期间，冷却未满，Allow 始终 false 且不提前转 half-open
	for i := 0; i < 3; i++ {
		cur = cur.Add(time.Minute) // 未满 5m
		if b.Allow() {
			t.Fatalf("open 冷却未满（+%d min）Allow 应为 false", i+1)
		}
		if b.state != model.BreakerOpen {
			t.Fatalf("冷却未满不应转态，got %v", b.state)
		}
	}
}
