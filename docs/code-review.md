# code-review — 实现代码审查

> 审查日期：2026-10-07。审查对象：`search-router` 全部实现代码（`cmd/`、`internal/`）。
> 审查目的：核对代码是否忠实实现五份设计文档（`design-review.md` 已闭环）与 `tasks.md`（`task-review.md` 已闭环）的决策，并找测试覆盖不到的 bug。
> 前置：`design-review.md`（设计三轮审查）、`task-review.md`（任务编排 + 可执行验收审查）。

---

## 一、验证结果

| 项 | 结果 |
|---|---|
| `go build ./...` | ✅ 通过 |
| `go vet ./...` | ✅ 无告警 |
| `go test ./...` | ✅ 全绿（breaker/config/keypool/providers/router/server 均有测试） |
| 安全 | ✅ `config.yaml` 已入 `.gitignore`，仓库只跟踪 `config.example.yaml`（占位 key），无真实 key 泄漏 |

## 二、正确实现的部分

前面三轮设计审查 + 任务审查定死的决策，代码逐条落地：

| 决策 | 位置 |
|---|---|
| 永久隔离（keyInvalid / 总配额 = 永久） | `pool.go:163-165`、`:226-228`（`qrPermanent`） |
| `MaxKeyAttempts` 含第一个 | `router.go:128`（`for i := 0; i < maxAttempts; i++`） |
| key 级耗尽不记熔断（仅 providerUnavailable 记） | `router.go:168-190` |
| 超时由 router 用 `context.WithTimeout` 包裹 | `router.go:142-151` |
| Meta 四字段构造（ignoredParams/degraded/switchedFrom/attempts） | `router.go:211-228`、`:250-271` |
| 假时钟 `var now`（同包测试覆盖） | `pool.go:16`、`breaker.go:17` |
| `Searcher` 窄接口（T7 可注入 fake） | `handler.go:15-17` |
| 错误归类 5 种 Kind + 502/400 映射 | `provider.go:28-49`、`handler.go:47-59` |
| SWRR 作用域 = 当前有资格 provider、掉出候选计数保留 | `swrr.go:19-41` |

## 三、中等问题（建议修）

| # | 位置 | 问题 |
|---|---|---|
| **1** | `router.go:214-217` | **`switchedFrom` 被 `circuit_open` 污染**。`switchedFrom` 取 `attempts[0].Provider`，但 `attempts[0]` 可能是「breaker 已开、根本没被选中」的 provider 的 `circuit_open` 记录（`router.go:61-66` 在动态过滤阶段就 append 了）。后果：某 provider 熔断中直接跳过、真正回答的是 B，`meta` 却报 `switchedFrom: A`（A 从未被选中）。同理 `Degraded: len(attempts)>1` 也把「跳过」误当「降级」。应基于「第一个真正被选中（非 circuit_open）的 provider」来算。 |
| **2** | `pool.go:166-168` | **上游被动配额耗尽丢失「总配额永久」区分**。`KindKeyQuotaExhausted` 无条件置 `qrMonthly`，但上游 402 若是「总配额用尽（一次性额度）」，下月边界会错误恢复、反复重试。config 里 Serper 标了 `totalQuota: 2500`，所以本地主动判（`refreshLocked` 的 `qrPermanent`）会先拦住，被动路径是兜底——但兜底逻辑本身语义不准。 |

## 四、轻微问题（可后续迭代）

| # | 位置 | 问题 |
|---|---|---|
| **3** | `breaker.go:51-60` | **half-open 允许多个并发探测，不是「一次探测」**。进入 half-open 后，所有 `Allow()` 都返回 true（`half-open != open`），直到首个 `RecordSuccess/RecordFailure` 才转态。设计写「放行一次探测」，这里放行了无限个——provider 仍宕机时会造成一波半开期洪峰。 |
| **4** | `config.go:62-71` | **`weight:0` / `priority:0` 无法表达**。`weight==0→1`、`priority==0→99` 把显式 0 也覆盖了。校验虽写 `≥0` 合法，但 0 实际设不进去（`weight:0` = split 下不分流；`priority:0` = failover 最高优先级）。要区分「未填」和「填 0」得用 `*int`。 |
| **5** | `config.go`（无 Timeout 默认） | **无默认 timeout**。`Timeout` 未填 = 0 → `router.go:144` 不包 `WithTimeout` → 上游请求可无限挂起。设计也没给默认值，但这是稳健性隐患，建议给个默认（如 10s）。 |

## 五、观察（非 bug，但值得记录）

1. **Serper 的 `totalQuota: 2500`（一次性，无月度重置）正是「总配额终身额度」场景**。上一轮结论是「不做持久化」，但 Serper 这种一次性额度，重启后本地计数清零 → 会继续白烧已死的 key 直到上游 402。上游会兜底（不花钱），但「主动分流」在重启后对 Serper 失效。是否要为 Serper 单独考虑持久化，待定。

2. **`timeRange` 无统一 schema**：同一个 `timeRange` 字符串被 serper/brightdata 当 `tbs`（Google 语法）、tavily 当 `days`（整数）、brave 直接忽略。单个值无法被各家正确解释，跨 provider 的 timeRange 过滤其实是坏的。这是设计层遗留，代码忠实复现了它。

## 六、结论与评价

**代码能用，且实现质量高**：

- **忠实性**：核心路由（主动分流、eligibility、SWRR、failover）、key 状态机（active/cooling/quarantined + 永久/边界恢复）、熔断（closed/open/half-open）、错误分类（5 种 Kind）、配置校验、超时、Meta 构造，全部与设计文档逐条对齐，无「设计与实现脱节」。
- **可测试性**：假时钟、`Searcher` 窄接口、mock adapter 都到位，验收清单 A1–A10 有对应测试。
- **安全**：真实 key 未进 git。

**开工前建议修 #1、#2**（影响响应元数据正确性和永久配额 key 行为）；#3~#5 是边缘稳健性，可后续迭代。观察 #1（Serper 一次性额度）需用户基于实际配额形态拍板是否补持久化。

---

## 七、修订记录（响应本次审查）

> 修订日期：2026-10-07。逐条处置 §三（中等问题）、§四（轻微问题）。

### 中等问题（已修）

| # | 项 | 处置 | 位置 |
|---|---|---|---|
| 1 | `switchedFrom` 被 `circuit_open` 污染 | 已修：`buildResponse` 剔除 `circuit_open`（熔断跳过记录），`switchedFrom` 改取「真正被选中的首个 attempt」的 provider；`Degraded` 改为真实尝试数 ≥2（跳过不再误当降级） | `router.go` `buildResponse`；`router_test.go` `TestA5_CircuitOpen_Recorded` 补断言 |
| 2 | 上游被动配额耗尽丢失「总配额永久」区分 | 已修：`ReportFailure` 对 `KindKeyQuotaExhausted` 判断 `cfg.TotalQuota != nil` → `qrPermanent`（一次性总配额永久隔离），否则 `qrMonthly` | `pool.go` `ReportFailure`；`pool_test.go` 新增 `TestA8_Quota_PassiveExhausted_TotalPermanent` |

### 轻微问题（未修，暂留后续迭代）

| # | 项 | 处置 | 理由 |
|---|---|---|---|
| 3 | half-open 放行多个并发探测 | 暂不修 | `Allow()`（资格过滤）与 `Record*`（请求后）解耦，严格「单探测」需探测预留标志，与现架构冲突；当前为半开期小洪峰，非正确性 bug |
| 4 | `weight:0`/`priority:0` 无法表达 | 不修 | 忠实 `data-model.md` 的 `Weight int`/`Priority int`；改 `*int` 破坏 T1「字段名/tag 逐字一致」 |
| 5 | 无默认 timeout | 待定 | 设计未给默认，`config.example.yaml` 已显式配 timeout；补默认（如 10s）改动极小，等拍板 |

### 验证

- `go build ./...` / `go vet ./...` / `go test ./...` 全绿；`go test -race ./internal/router/ ./internal/keypool/` 通过。
