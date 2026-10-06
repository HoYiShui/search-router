# api-contract — 接口契约

> v0.2 · 2026-10-06（含 design-review 修订）

最细粒度的一层：**签名级**定义，几乎完全定义行为。分「外部接口」「上游接口」「内部接口」。类型定义见 `data-model.md`。

## 外部接口

### `POST /search`

请求体（`model.SearchRequest`）：

```jsonc
{
  "query": "…",            // 必填
  "content": "any",        // 可选：any | body，缺省 any
  "site": "…",             // 可选
  "timeRange": "…",        // 可选
  "country": "…",          // 可选
  "lang": "…",             // 可选
  "page": 1,               // 可选
  "safeSearch": "off"      // 可选：off | moderate | strict
}
```

响应体（`model.SearchResponse`），示例为「serper 首个 key 429 → 降级到 tavily 成功」：

```jsonc
{
  "results": [
    { "title": "…", "url": "https://…", "content": "…", "contentType": "body", "score": 0.87, "publishedDate": "2024-03-05" }
  ],
  "meta": {
    "provider": "tavily",        // 实际回答的 provider
    "keyId": "tavily-k2",
    "tookMs": 812,
    "degraded": true,
    "switchedFrom": "serper",    // 最初选中 serper，跨 provider 降级到 tavily
    "ignoredParams": ["timeRange"],
    "attempts": [
      { "provider": "serper", "keyId": "serper-k1", "ok": false, "code": "keyRateLimited", "message": "429", "tookMs": 5 },
      { "provider": "tavily", "keyId": "tavily-k2", "ok": true, "tookMs": 807 }
    ]
  }
}
```

> 若只换 key（未换 provider），`switchedFrom` 留空、`degraded: true`（见 `data-model.md` 约束）。

HTTP 错误码：全部 provider 失败（`AllProvidersFailedError`）→ **502**；参数错（`badRequest`）→ **400**。

## 上游接口（adapter 的上游契约）

> 设计期约定；实现时须对照各 provider 最新官方文档核对字段与鉴权方式。

### serper（Google SERP）

- Endpoint：`POST https://google.serper.dev/search`
- 鉴权：Header `X-API-KEY: <key>`
- 请求映射：`Query→q`；`Page→page`；`Country→gl`；`Lang→hl`；`TimeRange→tbs`
- 响应 → `SearchResult`：`organic[]` → results；`title→Title`、`link→URL`、`snippet→Content`、`date→PublishedDate`
- `ContentType()`：`"abstract"`（无 `score`）

### tavily（AI 搜索）

- Endpoint：`POST https://api.tavily.com/search`
- 鉴权：Header `Authorization: Bearer <key>`
- 请求映射：`Query→query`；`TimeRange→days`。**无翻页**：`page>1` 软忽略（记入 `IgnoredParams`）；`max_results` 为网关固定值（如 5）
- 响应 → `SearchResult`：`results[]` → results；`title→Title`、`url→URL`、`content→Content`、`score→Score`、`published_date→PublishedDate`
- `ContentType()`：`"body"`（有 `score`）

### brave（Brave Search API）

- Endpoint：`GET https://api.search.brave.com/res/v1/web/search?q=…`
- 鉴权：Header `X-Subscription-Token: <key>`
- 请求映射：`Query→q`；`Page→offset`；`Country→country`；`SafeSearch→safesearch`
- 响应 → `SearchResult`：`web.results[]` → results；`title→Title`、`url→URL`、`description→Content`、`age→PublishedDate`
- `ContentType()`：`"abstract"`（无 `score`）

## 内部接口

### `providers.Provider`（adapter 契约）

```go
type Provider interface {
    ID() string
    // 发上游请求；失败时返回 *model.ProviderError（带 Kind），供 router 分类
    Search(ctx context.Context, req model.SearchRequest, key model.Key) ([]model.SearchResult, error)
    Capabilities() []string // 软能力列表（含 "page"）
    ContentType() string    // 硬能力："abstract" | "body"
}
```

> 翻页能力并入 `Capabilities()` 的 `"page"`，不再单设 `SupportsPaging()`。

### `keypool.Pool`

```go
var ErrNoKeyAvailable = errors.New("no key available")

type Pool struct { /* 内部含 sync.Mutex 保护共享态 */ }
func NewPool(keys []model.KeyConfig, opts PoolOptions) *Pool

func (p *Pool) Acquire() (model.Key, error)                       // 无可用 key 返回 ErrNoKeyAvailable
func (p *Pool) ReportSuccess(id string)                            // 清连续失败
func (p *Pool) ReportFailure(id string, err *model.ProviderError)  // 按 err.Kind 置 state
func (p *Pool) HasCapacity() bool                                  // 是否至少一个 key 可发
```

### `breaker.Breaker`

```go
type Breaker struct { /* ... */ }
func NewBreaker(opts BreakerOptions) *Breaker

func (b *Breaker) Allow() bool          // open 期间返回 false
func (b *Breaker) RecordSuccess()       // 半开探测成功 → closed
func (b *Breaker) RecordFailure()       // 连续失败达阈值 → open
```

### `router.Router`

```go
type ProviderEntry struct {
    Config  model.ProviderConfig // weight / priority / contentType / timeout / maxKeyAttempts…
    Adapter providers.Provider
    Pool    *keypool.Pool
    Breaker *breaker.Breaker
}

type Router struct { /* ... */ }
func NewRouter(entries []ProviderEntry, mode string) *Router

// 内部完成「选 provider → 取 key → 失败重选（≤MaxKeyAttempts）」整个循环；全部失败返回 *model.AllProvidersFailedError
func (r *Router) Search(ctx context.Context, req model.SearchRequest) (*model.SearchResponse, error)
```

### `config`

```go
func Load(path string) (*model.Config, error) // 读 yaml，校验 qps>0 等约束
// 路径来自 env SEARCH_ROUTER_CONFIG，缺省 ./config.yaml
```

### `server`

```go
func NewHandler(rt *router.Router) http.Handler // 绑定 POST /search，JSON 编解码 + 错误码映射（502/400）
```

> 约定：所有上游错误以 `model.ProviderError` 表达，`Kind` 取值见 `operation.md`；`model` 包被所有包依赖（无环）。
