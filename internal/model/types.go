// Package model holds shared types used across all packages.
// It is the dependency-free core: config entities (static yaml),
// runtime entities (in-memory state), and request/response entities.
package model

import "time"

// ---- 配置实体（静态） ----

type Config struct {
	Routing   RoutingConfig    `yaml:"routing"`
	Providers []ProviderConfig `yaml:"providers"`
}

type RoutingConfig struct {
	Mode string `yaml:"mode"` // "split" | "failover"，缺省 "split"
}

// Routing mode 取值
const (
	ModeSplit    = "split"
	ModeFailover = "failover"
)

type ProviderConfig struct {
	ID             string        `yaml:"id"`
	Enabled        bool          `yaml:"enabled"`
	Weight         int           `yaml:"weight"`         // split 流量份额，缺省 1
	Priority       int           `yaml:"priority"`       // failover 排序，缺省 99
	Timeout        time.Duration `yaml:"timeout"`        // 上游超时，yaml 写字符串如 "10s"
	MaxKeyAttempts int           `yaml:"maxKeyAttempts"` // 同 provider 内最多尝试的 key 数（含第一个），缺省 3
	Capabilities   []string      `yaml:"capabilities"`   // 软能力（含 page）：timeRange/site/country/lang/page/safeSearch
	ContentType    string        `yaml:"contentType"`    // 硬能力："abstract" | "body"
	Zone           string        `yaml:"zone,omitempty"` // Bright Data SERP 的 zone（仅 brightdata 用，其他 provider 忽略）
	Keys           []KeyConfig   `yaml:"keys"`
}

type KeyConfig struct {
	ID           string `yaml:"id"`
	Label        string `yaml:"label"`
	Value        string `yaml:"value"` // API key 本体
	Enabled      bool   `yaml:"enabled"`
	QPS          int    `yaml:"qps"`          // > 0
	DailyQuota   *int   `yaml:"dailyQuota"`   // nil = 不限
	MonthlyQuota *int   `yaml:"monthlyQuota"` // nil = 不限
	TotalQuota   *int   `yaml:"totalQuota"`   // nil = 不限
}

// ---- 运行时实体（内存态） ----

type KeyState string

const (
	KeyActive      KeyState = "active"
	KeyCooling     KeyState = "cooling"
	KeyQuarantined KeyState = "quarantined"
)

type KeyRuntime struct {
	ID            string
	State         KeyState
	Tokens        float64 // 令牌桶当前余量
	LastRefillAt  time.Time
	UsedToday     int
	UsedMonth     int
	UsedTotal     int
	DayStamp      string // UTC 日期 "2006-01-02"
	MonthStamp    string // UTC 月份 "2006-01"
	CooldownUntil time.Time
	LastError     string
	LastErrorKind ErrorKind
}

type BreakerState int

const (
	BreakerClosed BreakerState = iota
	BreakerOpen
	BreakerHalfOpen
)

type BreakerRuntime struct {
	State         BreakerState
	FailureCount  int
	OpenedAt      time.Time
	CooldownUntil time.Time
}

// ---- 请求 / 响应实体 ----

// Content 取值（请求偏好）
const (
	ContentAny  = "any"  // 缺省：摘要或全文都行
	ContentBody = "body" // 只要全文
)

// ContentType 取值（结果形态 + provider 硬能力）
const (
	ContentTypeAbstract = "abstract" // 摘要
	ContentTypeBody     = "body"     // 全文
)

// SafeSearch 取值（闭集）
const (
	SafeSearchOff      = "off" // 缺省
	SafeSearchModerate = "moderate"
	SafeSearchStrict   = "strict"
)

type SearchRequest struct {
	Query      string `json:"query"`
	Content    string `json:"content,omitempty"` // "any"(缺省) | "body"
	Site       string `json:"site,omitempty"`
	TimeRange  string `json:"timeRange,omitempty"`
	Country    string `json:"country,omitempty"`
	Lang       string `json:"lang,omitempty"`
	Page       int    `json:"page,omitempty"`
	SafeSearch string `json:"safeSearch,omitempty"` // "off" | "moderate" | "strict"
}

type SearchResult struct {
	Title         string   `json:"title"`
	URL           string   `json:"url"`             // 用于引用 / 二次取全文
	Content       string   `json:"content"`         // 正文或摘要
	ContentType   string   `json:"contentType"`     // "abstract" | "body"
	Score         *float64 `json:"score,omitempty"` // 仅部分 provider 有，不跨 provider 归一
	PublishedDate *string  `json:"publishedDate,omitempty"`
}

type SearchResponse struct {
	Results []SearchResult `json:"results"`
	Meta    Meta           `json:"meta"`
}

type Meta struct {
	Provider      string    `json:"provider"` // 实际回答的 provider
	KeyID         string    `json:"keyId"`    // 实际使用的 key
	TookMs        int64     `json:"tookMs"`
	Degraded      bool      `json:"degraded"`                // 是否发生降级（换过 key 或 provider）
	SwitchedFrom  string    `json:"switchedFrom,omitempty"`  // 最初选中的 provider；仅当跨 provider 降级时填充
	IgnoredParams []string  `json:"ignoredParams,omitempty"` // 被忽略的软参数
	Attempts      []Attempt `json:"attempts"`
}

type Attempt struct {
	Provider string `json:"provider"`
	KeyID    string `json:"keyId,omitempty"`
	OK       bool   `json:"ok"`
	Code     string `json:"code"` // 见「attempt code 与 ErrorKind 的分层」
	Message  string `json:"message,omitempty"`
	TookMs   int64  `json:"tookMs"`
}

// Key 是运行时选中的 key，adapter 只用 Value 发请求
type Key struct {
	ID    string
	Label string
	Value string
}
