# tasks — 开发任务拆分

> 由五份设计文档拆分而来。任务按**依赖顺序**排列，每个任务标注：输入文档、产出文件、验收锚点（对应 `operation.md` 的 A1–A10）、完成定义。
> 依赖图：`model` → `config`/`breaker`/`keypool` → `providers` → `router` → `server` → `main` → 验收测试。

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
- **完成**：`go build ./...` 通过

## T1 `model` 包

- **输入**：`data-model.md` 全部
- **产出**：`internal/model/types.go`、`internal/model/errors.go`
- **内容**：`Config` / `RoutingConfig` / `ProviderConfig` / `KeyConfig` / `Key` / `KeyRuntime` / `KeyState` / `BreakerRuntime` / `BreakerState` / `SearchRequest` / `SearchResult` / `SearchResponse` / `Meta` / `Attempt`；常量（`ContentAny/Body`、`ContentTypeAbstract/Body`、`SafeSearchOff/Moderate/Strict`、`KeyActive/Cooling/Quarantined`、`BreakerClosed/Open/HalfOpen`、5 个 `Kind*`）；`ProviderError` / `AllProvidersFailedError`
- **完成**：字段名 + JSON/YAML tag 与 `data-model.md` 逐字一致；`go build` 通过

## T2 `config` 包

- **输入**：`data-model.md` 配置实体、`architecture.md` 核心决策
- **产出**：`internal/config/config.go`
- **内容**：`Load(path string) (*model.Config, error)` 读 yaml；路径来自 env `SEARCH_ROUTER_CONFIG`（缺省 `./config.yaml`）；校验 `qps > 0`；填缺省（`weight=1`、`priority=99`、`maxKeyAttempts=3`、`mode="split"`）
- **完成**：非法配置（如 qps=0）报错；缺省值正确

## T3 `breaker` 包

- **输入**：`operation.md` 熔断状态机、`api-contract.md` breaker 契约
- **产出**：`internal/breaker/breaker.go`
- **内容**：`Breaker{ Allow / RecordSuccess / RecordFailure }`；状态机 `closed → open → half-open → closed`；阈值与冷却时长可配；`sync.Mutex` 保护
- **验收**：**A6**（连续失败≥阈值→open；冷却→half-open；探测成功→closed）
- **完成**：注入假时钟驱动状态机，断言 `Allow()` 在各状态的返回值

## T4 `keypool` 包

- **输入**：`operation.md` key 状态机 + 配额 reset、`data-model.md` `KeyRuntime`、`api-contract.md` Pool 契约
- **产出**：`internal/keypool/pool.go`
- **内容**：`Pool{ Acquire / ReportSuccess / ReportFailure / HasCapacity }` + `ErrNoKeyAvailable`；轮询 + 令牌桶(QPS) + 日/月/总配额；key 状态机（429→cooling、401/403→quarantined **永久**、日/月配额→quarantined 到边界回 active、总配额→**永久**）；惰性 reset（UTC 日/月戳）；`sync.Mutex` 保护
- **验收**：**A2**（HasCapacity）、**A7**（状态机）、**A8**（配额 reset）
- **完成**：可控时钟下单测覆盖全部状态转移与 reset 边界

## T5 `providers` 包

- **输入**：`api-contract.md` 上游接口、`data-model.md` `SearchResult`
- **产出**：`internal/providers/provider.go`（接口）+ `serper.go` / `tavily.go` / `brave.go`
- **内容**：`Provider{ ID / Search / Capabilities / ContentType }` 接口；三个 adapter 按上游契约实现（endpoint / 鉴权头 / 请求映射 / 响应→`SearchResult` 翻译）；错误归类为 `*model.ProviderError`
- **验收**：**A10**（归一化正确）
- **完成**：每 provider 一份样本响应，断言字段映射 + `contentType` 正确；实现时对照各 provider 官方文档核对

## T6 `router` 包

- **输入**：`dataflow.md` §2、`api-contract.md` Router 契约
- **产出**：`internal/router/router.go`、`internal/router/swrr.go`
- **内容**：平滑加权轮询(SWRR)；`ProviderEntry{Config, Adapter, Pool, Breaker}`；eligibility 过滤（启用 + 熔断未开 + `HasCapacity` + 硬能力 `content`）；split / failover 两种模式；内层 `MaxKeyAttempts` 有界循环；key 级故障耗尽**不**记熔断
- **验收**：**A1**（weight 分布）、**A2**（资格过滤）、**A3**（硬能力）、**A4**（换 key ≤ 上限）、**A5**（换 provider / badRequest 不重试）
- **完成**：mock adapter 单测覆盖路由选择、降级路径、`Attempt` 记录

## T7 `server` 包

- **输入**：`api-contract.md` 外部接口
- **产出**：`internal/server/handler.go`
- **内容**：`NewHandler(rt *router.Router) http.Handler`；绑定 `POST /search`；JSON 编解码；错误码映射（`AllProvidersFailedError`→502、`badRequest`→400）
- **验收**：**A9**
- **完成**：集成测断言 200 / 502 / 400 三种状态码与响应体

## T8 `main` 组装

- **产出**：`cmd/search-router/main.go`
- **内容**：`config.Load` → 构造 adapter/pool/breaker → 组装 `[]ProviderEntry` → `NewRouter` → `NewHandler` → `http.ListenAndServe`
- **完成**：`go build` 产出单二进制，`./search-router` 启动可响应 `/search`

## T9 验收测试

- **产出**：各包 `*_test.go`
- **内容**：按 `operation.md`「验收清单」A1–A10 逐条写单测/集成测
- **完成**：`go test ./...` 全绿，且每条 A1–A10 有对应测试、测试名可追溯
