# tasks — 开发任务拆分

> 由五份设计文档拆分而来。任务按**依赖顺序**排列，每个任务自带**可执行的完成条件**（具体命令 + 覆盖行为），可独立交给 goal 模式判定完成。
> 依赖图：`model` → `config`/`breaker`/`keypool` → `providers` → `router` → `server` → `main` → 验收测试。
> 验收锚点 A1–A10 定义见 `operation.md`「验收清单」。

## 总验收（gate）

分两档：**自动化门槛**（可进 CI，无需真 key）与**人工冒烟**（需真 key + 上游在线，发布前跑一次）。

### 自动化门槛（机器判定）

1. `go build ./...` 成功，产出单二进制 `search-router`。
2. `go test ./...` 全绿，覆盖 A1–A10 全部条目（每条至少一个测试，测试名可追溯到 A 编号）。
3. `go vet ./...` 无告警。

### 人工冒烟（真 key，发布前）

4. `./search-router` 读真实 `config.yaml` 启动，`curl -X POST localhost:8080/search -d '{"query":"golang"}'` 返回 200、`results` 非空。
5. 配多 key 的 provider，并发打满 QPS 后请求自动分流到下一家（对应 A1/A2 的手工验证）。

> 人工检查项（不阻塞 CI，交接时核对）：字段名/tag 与 `data-model.md` 逐字一致（T1）、测试名可追溯 A 编号（T9）。

## 总览

| # | 任务 | 依赖 | 验收锚点 |
|---|---|---|---|
| T0 | 脚手架 | — | — |
| T1 | `model` 包 | — | — |
| T2 | `config` 包 | T1 | — |
| T3 | `breaker` 包 | T1 | A6 |
| T4 | `keypool` 包 | T1 | A2 / A7 / A8 |
| T5 | `providers` 包 | T1 | A10 |
| T6 | `router` 包 | T3 / T4 / T5 | A1–A5 |
| T7 | `server` 包 | T6 | A9 |
| T8 | `main` 组装 | T7 | — |
| T9 | 验收测试 | 全部 | A1–A10 |

---

## T0 脚手架

- **产出**：`go.mod`（module `search-router`）、目录树、`cmd/search-router/main.go`（空 main）
- **完成**：`go build ./...` 退出码 0

## T1 `model` 包

- **输入**：`data-model.md` 全部
- **产出**：`internal/model/types.go`、`internal/model/errors.go`
- **内容**：`Config` / `RoutingConfig` / `ProviderConfig`（含 `Zone`）/ `KeyConfig` / `Key` / `KeyRuntime` / `KeyState` / `BreakerRuntime` / `BreakerState` / `SearchRequest` / `SearchResult` / `SearchResponse` / `Meta` / `Attempt`；常量（`ContentAny/Body`、`ContentTypeAbstract/Body`、`SafeSearchOff/Moderate/Strict`、`KeyActive/Cooling/Quarantined`、`BreakerClosed/Open/HalfOpen`、5 个 `Kind*`）；`ProviderError` / `AllProvidersFailedError`
- **完成**：`go build ./...` 退出码 0 且 `go vet ./internal/model/` 无告警；字段名 + JSON/YAML tag 与 `data-model.md` 逐字一致

## T2 `config` 包

- **输入**：`data-model.md` 配置实体、`architecture.md` 核心决策
- **产出**：`internal/config/config.go`；`config.example.yaml`（既有仓库模板，作为 Load 解析验证目标）
- **内容**：`Load(path string) (*model.Config, error)` 读 yaml；路径来自 env `SEARCH_ROUTER_CONFIG`（缺省 `./config.yaml`）；校验 `qps>0`、`mode∈{split,failover}`、`contentType∈{abstract,body}`、`weight≥0`、`priority≥0`；填缺省（`weight=1`、`priority=99`、`maxKeyAttempts=3`、`mode="split"`）
- **完成**：`go test ./internal/config/` 通过，覆盖：`qps=0` 报错、`mode`/`contentType` 非法值报错、缺省值正确（含解析 `config.example.yaml` 成功）

## T3 `breaker` 包

- **输入**：`operation.md` 熔断状态机、`api-contract.md` breaker 契约
- **产出**：`internal/breaker/breaker.go`
- **内容**：`Breaker{ Allow / RecordSuccess / RecordFailure }` + `BreakerOptions{Threshold=3, Cooldown=5m}`；状态机 `closed → open → half-open → closed`；`sync.Mutex` 保护；`Allow()` 返回 false 时调用方记 `Attempt{Code:"circuit_open"}`
- **验收**：**A6**
- **完成**：`go test ./internal/breaker/` 通过，假时钟注入（包内 `var now = time.Now`，同包测试覆盖），覆盖 `closed→open→half-open→closed` 及 `half-open→open`（探测失败）全转移 + open 期间 `Allow()==false`

## T4 `keypool` 包

- **输入**：`operation.md` key 状态机 + 配额 reset、`data-model.md` `KeyRuntime`、`api-contract.md` Pool 契约
- **产出**：`internal/keypool/pool.go`
- **内容**：`Pool{ Acquire / ReportSuccess / ReportFailure / HasCapacity }` + `ErrNoKeyAvailable` + `PoolOptions{RateLimitCooldown=1s}`；轮询 + 令牌桶(QPS) + 日/月/总配额；key 状态机（429→cooling、401/403→quarantined **永久**、日/月配额→quarantined 到边界回 active、总配额→**永久**）；配额两层（本地 `refresh` 主动判 + 上游 `ReportFailure` 被动判）；惰性 reset（UTC 日/月戳）；`sync.Mutex` 保护；`Acquire` 抛 `ErrNoKeyAvailable` 时调用方记 `Attempt{Code:"no_key_available"}`
- **验收**：**A2 / A7 / A8**
- **完成**：`go test ./internal/keypool/` 通过（假时钟：包内 `var now = time.Now`，同包测试覆盖），覆盖：轮询顺序、令牌桶 refill、三档配额、key 状态机全部转移、UTC 日/月边界 reset、`HasCapacity`

## T5 `providers` 包

- **输入**：`api-contract.md` 上游接口、`data-model.md` `SearchResult`
- **产出**：`internal/providers/provider.go`（接口）+ `serper.go` / `tavily.go` / `brave.go` / `brightdata.go`
- **内容**：`Provider{ ID / Search / Capabilities / ContentType }` 接口；四个 adapter 按上游契约实现（endpoint / 鉴权头 / 请求映射 / 响应→`SearchResult` 翻译）；错误归类为 `*model.ProviderError`（上游配额错误归类 `KindKeyQuotaExhausted`）
- **验收**：**A10**
- **完成**：`go test ./internal/providers/...` 通过，每 provider 一份样本响应断言字段映射 + `contentType` 正确（serper/brightdata/brave=abstract，tavily=body）

## T6 `router` 包

- **输入**：`dataflow.md` §2、`api-contract.md` Router 契约
- **产出**：`internal/router/router.go`、`internal/router/swrr.go`
- **内容**：平滑加权轮询(SWRR，作用域=当前有资格 provider，掉出候选不参与本轮、计数保留)；`ProviderEntry{Config, Adapter, Pool, Breaker}`；eligibility 过滤（启用 + 熔断未开 + `HasCapacity` + 硬能力 `content`）；split / failover 两种模式；内层 `MaxKeyAttempts` 有界循环；key 级故障耗尽**不**记熔断；**timeout**：`context.WithTimeout(ctx, entry.Config.Timeout)` 包裹 `adapter.Search`；**Meta 构造**：计算 `IgnoredParams`、填充 `Degraded`/`SwitchedFrom`（跨 provider 才填）、记录 `Attempts`
- **验收**：**A1–A5**
- **完成**：`go test ./internal/router/` 通过，mock adapter 覆盖：SWRR 权重分布（足够样本 ≥1000 次 + 容差 ±5%）、资格过滤、硬能力 `content=body`、`MaxKeyAttempts` 上界、降级路径、Meta 四字段构造

## T7 `server` 包

- **输入**：`api-contract.md` 外部接口
- **产出**：`internal/server/handler.go`
- **内容**：`NewHandler(s Searcher) http.Handler`（`Searcher` 为窄接口 `Search(ctx, req) (*SearchResponse, error)`，`*router.Router` 自然满足）；绑定 `POST /search`；JSON 编解码；错误码映射（`AllProvidersFailedError`→502、`badRequest`→400）
- **验收**：**A9**
- **完成**：`go test ./internal/server/` 通过，注入 fake `Searcher`，覆盖 200 / 502 / 400 三种状态码 + 响应体 JSON 结构

## T8 `main` 组装

- **产出**：`cmd/search-router/main.go`
- **内容**：`config.Load` → 构造 adapter/pool/breaker → 组装 `[]ProviderEntry` → `NewRouter(entries, cfg.Routing.Mode)` → `NewHandler` → `http.ListenAndServe(":8080", handler)`
- **完成**：`go build ./...` 产出单二进制；`./search-router` 启动后可响应（人工冒烟见「总验收」§人工冒烟 #4）

## T9 验收测试

- **产出**：各包 `*_test.go`
- **内容**：按 `operation.md`「验收清单」A1–A10 逐条写单测/集成测
- **完成**：`go test ./...` 全绿；每条 A1–A10 有对应测试且测试名可追溯到 A 编号
