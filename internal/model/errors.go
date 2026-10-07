package model

import "time"

// ErrorKind 是上游错误类别（5 个），由 adapter 抛出，
// 供 router 决定「换 key / 换 provider / 不重试」。
type ErrorKind string

const (
	KindKeyInvalid          ErrorKind = "keyInvalid"          // 401/403
	KindKeyRateLimited      ErrorKind = "keyRateLimited"      // 429
	KindKeyQuotaExhausted   ErrorKind = "keyQuotaExhausted"   // 配额耗尽
	KindProviderUnavailable ErrorKind = "providerUnavailable" // 5xx / 超时 / 网络错
	KindBadRequest          ErrorKind = "badRequest"          // 400 参数错
)

type ProviderError struct {
	Kind       ErrorKind
	Message    string
	RetryAfter time.Duration // 仅 keyRateLimited 用
}

func (e *ProviderError) Error() string { return string(e.Kind) + ": " + e.Message }

type AllProvidersFailedError struct {
	Attempts []Attempt
}

func (e *AllProvidersFailedError) Error() string { return "all providers failed" }
