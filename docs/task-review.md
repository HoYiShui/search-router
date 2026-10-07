# task-review — 开发任务编排审查

> 审查日期：2026-10-06。审查对象：`docs/tasks.md`（T0–T9）。
> 审查目标：核对 task 编排与设计是否足够支撑五份设计文档（architecture / dataflow / data-model / api-contract / operation）描述的实现。
> 前置：五份设计文档已完成三轮一致性审查（见 `design-review.md`）。

---

## 一、总体结论

tasks.md 的**编排是正确的**：

- **依赖顺序正确**：`model` → `config`/`breaker`/`keypool`/`providers` → `router` → `server` → `main`，与 architecture 的依赖朝向一致。
- **包覆盖完整**：architecture 的 8 个包（server/router/keypool/breaker/providers/config/model/main）全部有对应任务。
- **验收锚点完整**：A1–A10 全部分配到任务，无遗漏。
- **范围裁剪正确**：「可选 SQLite 持久化」「暂不做的 Redis/多实例」被正确排除，未误入任务。
- **吸收了三轮 design-review 结论**：T4「401/403→quarantined 永久」「总配额→永久」、T6「key 级故障耗尽不记熔断」均正确反映 R2/R3 最终决策。

但 tasks.md 把设计文档里**仍潜伏的悬空引用/无归属问题**暴露成了实现障碍。分三档记录。

---

## 二、阻塞级缺口（不补，对应任务无法落笔）

| # | 级别 | 位置 | 问题 |
|---|---|---|---|
| **1** | 中 | `api-contract.md:102/114` → `tasks.md` T3/T4 | `BreakerOptions`、`PoolOptions` 是**悬空引用**：api-contract 写了 `NewBreaker(opts BreakerOptions)`、`NewPool(keys, opts PoolOptions)`，但这俩类型的字段**从未在 data-model / operation 定义**。T3 说「阈值与冷却时长可配」、T4 的令牌桶 / 隔离参数，都依赖这些不存在的字段。实现者不知道熔断阈值、冷却时长、令牌桶容量、隔离时长「从哪来、叫什么、默认多少」。**建议拆 T3/T4 前，先在 data-model 定义 `BreakerOptions` / `PoolOptions` 字段 + 默认值。** |
| **2** | 中 | `operation.md §超时` + `architecture.md:12` → 无任务归属 | **超时 + context 贯穿没有 owner**：operation 说「每 provider 可配 Timeout，用 `context.WithTimeout` 实现」，architecture / operation 都说「context 贯穿 server→router→adapter」。但 T5（adapter 发上游）还是 T6（router）负责包 timeout？`Timeout` 在 `ProviderConfig` 里，可没有任何任务写「`context.WithTimeout(ctx, Config.Timeout)` 包裹上游调用，超时→providerUnavailable」。**建议在 T5 或 T6 的「内容」里明确这一条。** |

---

## 三、轻量缺口（会走样，但能硬着头皮写）

| # | 级别 | 位置 | 问题 |
|---|---|---|---|
| **3** | 轻 | `dataflow.md:11` → `tasks.md` T6 | **`ignoredParams` 无归属**：dataflow 说软参数「静默忽略，记入 `Meta.IgnoredParams`」，但 T6 的「内容」只提 eligibility / 模式 / 循环，没提计算 `ignoredParams`；`degraded` / `switchedFrom` 的填充规则也只在 data-model 约束里，T6 未把它列为交付物。Meta 的四个字段（ignoredParams / degraded / switchedFrom / attempts）只有 attempts 在「完成」里点到。 |
| **4** | 轻 | `tasks.md` T2 | **校验面过窄**：T2 只写「校验 qps>0」，但 data-model 还隐含 mode∈{split,failover}、contentType∈{abstract,body}、weight/priority 边界等校验。建议 T2 列全校验清单，或明确指向 data-model 约束。 |
| **5** | 轻 | `dataflow.md:8` → `tasks.md` T6 `swrr.go` | **SWRR 无 spec**：dataflow 只说「按 weight 用 SWRR」，没写 SWRR 的作用域（只在有资格 provider 内）、状态复位规则（provider 掉出候选后 SWRR 计数器怎么处理）。T6 产出 swrr.go 却无算法输入。 |
| **6** | 轻 | `operation.md:77` ↔ `tasks.md` T4 | **R3-1 现在反噬 tasks**：T4 正确写「总配额→永久」，但 operation.md:77 仍是旧的「因配额耗尽…到下一边界自动回 active」。实现 T4 时读 operation 会直接撞上矛盾。这正是 design-review 里「随任务写死」的 R3-1，现在它已经和任务 spec 冲突了，**建议现在就修掉 `operation.md:77`**。 |

---

## 四、极轻缺口（口头 / 一句补丁即可）

| # | 位置 | 问题 |
|---|---|---|
| **7** | `tasks.md` T8 | `NewRouter(entries, mode)`，T8 说 `config.Load → NewRouter`，但没写「把 `config.Routing.Mode` 传入」。 |
| **8** | `data-model.md §分层` → T3/T4/T6 | 本地码产出点未归属：data-model 说 `no_key_available` / `circuit_open` 由 keypool / breaker 产生、router 记录；T6 有「Attempt 记录」，但 T3 / T4 没提「产生这两个码」。 |
| **9** | `operation.md §错误分类` → T4/T5 | `keyQuotaExhausted` 归属未定：配额耗尽到底是 adapter 从上游信号归类，还是 keypool 本地记账主动判出（不真发请求）？operation 表格没划清，T4 / T5 都没接这块。 |

---

## 五、结论与建议

- **#1、#2 是真正会卡住实现的缺口**，且根子在设计文档（`BreakerOptions` / `PoolOptions` 悬空、timeout 无归属），不是 tasks.md 自己能补的。建议先回炉五份文档补上这两处，再同步 tasks.md。
- #3～#6 是 tasks.md 自身可补的：给 T6 加 Meta 构造交付物、T2 列全校验、T6 补 SWRR 作用域、修 `operation.md:77`。
- #7～#9 极轻，可随任务条目一句话收敛。

**编排层面（依赖 / 覆盖 / 锚点 / 裁剪）已经达标；差距集中在「少数悬空配置项」和「横切行为的任务归属」两处。** 补上 #1、#2 后，tasks.md 即可作为开工的可靠依据。

---

## 六、修订记录（响应 task-review 第一轮）

> 修订日期：2026-10-06。

### 阻塞级

| 项 | 决策 | 位置 |
|---|---|---|
| #1 BreakerOptions / PoolOptions 悬空 | 在 api-contract 定义两 struct + 默认值（`BreakerOptions{Threshold=3, Cooldown=5m}`、`PoolOptions{RateLimitCooldown=1s}`），并同步进 T3/T4 | `api-contract.md` + `tasks.md` T3/T4 |
| #2 timeout 无归属 | 定死：**router（T6）** 用 `context.WithTimeout(ctx, ProviderConfig.Timeout)` 包裹 `adapter.Search` | `operation.md`「超时」+ `tasks.md` T6 |

### 轻量

| 项 | 决策 | 位置 |
|---|---|---|
| #3 ignoredParams/degraded/switchedFrom 无归属 | T6 增「Meta 构造」交付物：`IgnoredParams` 计算 + `Degraded`/`SwitchedFrom` 填充 + `Attempts` 记录 | `tasks.md` T6 |
| #4 T2 校验过窄 | 扩为 `qps>0`、`mode∈{split,failover}`、`contentType∈{abstract,body}`、`weight≥0`、`priority≥0` | `tasks.md` T2 |
| #5 SWRR 无 spec | 定作用域=当前有资格 provider、掉出候选不参与本轮且计数保留 | `dataflow.md` §2 + `tasks.md` T6 |
| #6 operation.md:77 反噬 | **核实为审阅方行号过期**：该处已在 R3-1 改为「日/月配额…到边界回 active（总配额除外，永久）」，无需再改 | — |

### 极轻

| 项 | 决策 | 位置 |
|---|---|---|
| #7 T8 未传 mode | 改为 `NewRouter(entries, cfg.Routing.Mode)` | `tasks.md` T8 |
| #8 本地码产出点未归属 | T3 标注产出 `circuit_open`、T4 标注产出 `no_key_available` | `tasks.md` T3/T4 |
| #9 keyQuotaExhausted 归属未定 | 明确两层：本地 `refresh` 主动判 + 上游 adapter 归类 `keyQuotaExhausted`→`ReportFailure` | `operation.md` + `tasks.md` T4/T5 |

### 结论

#1/#2 已回炉设计文档补齐，其余在 tasks.md 收敛；#6 经核实为过期行号（R3-1 已修，无需改动）。tasks.md 现可作为开工依据。

---

## 七、勘误注（§六 #6 归因修正）

> 复核日期：2026-10-07。

§六 表格 #6 的「决策」列写着「**核实为审阅方行号过期**：该处已在 R3-1 改为…无需再改」，此归因**不准确**，特此勘误：

- **事实**：三轮审查（`design-review.md` §八）当时读到的 `operation.md:77` **确为旧的笼统表述**「因配额耗尽…到下一边界自动回 active」，R3-1 **不是误报**，行号也**未过期**。
- **修复时机**：`operation.md:78` 改为「因**日/月**配额耗尽…（总配额除外，永久）」，以及 key 状态机转移表补「重启进程清态」行（对应 R3-2），均发生在**本轮**（响应 task-review），而非「已在 R3-1 改」。
- **结论不变**：`operation.md` 现已正确，R3-1/R3-2 两个真实发现已闭环。仅修订记录的归因表述需更正，避免后人误读为「三轮审查报过错」。

即 §六 #6 应作如下如实表述：

> `operation.md:78` 已改为「日/月配额…到边界回 active（总配额除外，永久）」，转移表补「重启进程」行 —— 本轮修复 R3-1 / R3-2（不是审阅方误报）。

---

## 八、修订记录（二轮追加项）

> 修订日期：2026-10-07。

### 追加修复

| # | 决策 | 位置 |
|---|---|---|
| 监听端口未定 | 监听地址定死 `:8080`（单二进制默认端口） | `architecture.md` 技术形态 + `tasks.md` T8 |
| `ConsecutiveFailures` 悬空 | **删除**（无 key 级熔断，该字段无消费者）；`ReportSuccess` 改「清 LastError / LastErrorKind」 | `data-model.md` KeyRuntime + `api-contract.md` ReportSuccess |

### 关于 §七 勘误的时间线澄清

经核对本会话操作历史：`operation.md:78`（日/月配额）与转移表「重启进程」行的修改，**实际发生在响应 design-review §八（R3-1/R3-2）的那一轮**——当轮已用 grep 验证「因配额耗尽」无残留、转移表补第 7 行。故 §六 #6 的原始归因「该处已在 R3-1 改」是**准确的**；§七 所称「本轮修复、非审阅方误报」的时间线判定有误。

需澄清的边界：R3-1/R3-2 **本身是真实发现**（round-3 时 operation.md:77 确为旧表述），非误报；只是修复发生在 R3 响应轮、而非 task-review 轮。这是时间线元数据之争，**不影响文档实质正确性**（operation.md 当前已正确）。

---

## 九、可执行验收条件审查

> 复核日期：2026-10-07。审查对象：`tasks.md` 新增的「总验收 gate」与各 task 的「完成」条件（已从模糊表述升级为具体命令 + 覆盖清单）。

### 前置核对

`tasks.md` 新出现的 `Zone`（T1）与 `brightdata`（T5）**并非悬空**——设计文档本轮已同步：`data-model.md:33` 加 `Zone`、`architecture.md:23/38` 加 `brightdata`、`api-contract.md:80-89` 补 brightdata 上游契约、`data-model.md:225` 的 ContentType 映射同步为「Serper / Brave / Bright Data → abstract」。此层无漂移。

### 总体评价

方向对、是实质升级：从「完成：mock adapter 单测覆盖…」变为「`go test ./internal/X/` + 具体覆盖项」。验收目的（每个 task 有明确"什么算做完"信号）**基本能达成**，A1–A10 覆盖完整。

但按「可执行 + 能达成验收」两个标准，有几处需在开工前补，否则会出现「功能写完、验收测试写不出来/写不稳」。

### 中 —— 非密闭验收混进硬门槛

| # | 位置 | 问题 |
|---|---|---|
| 1 | `tasks.md` gate #3（:13）/ #4（:14）、T8（:96） | `curl localhost:8080/search`、并发打满 QPS 分流，都依赖**真实 config.yaml（真 key）+ 真网络 + 上游在线**，非密闭。与 gate #1/#2（build/test，可进 CI）混在「硬门槛」里。建议拆成「自动化门槛」（build/vet/test 全绿）与「人工冒烟」（真 key，发布前跑一次），否则无真 key 的环境（CI/交接）会卡 gate。 |

### 轻 —— 测试可行性有坑

| # | 位置 | 问题 |
|---|---|---|
| 2 | T7 + `api-contract.md:159` | **502/400 测试难注入**：`NewHandler(rt *router.Router)` 收具体类型 `*router.Router` 而非接口。要测 `AllProvidersFailedError→502` 就得构造"全部 provider 失败"的真实 router，把 T7 测试退化成集成测试、模糊 T6/T7 边界。建议 `NewHandler` 收窄接口（如 `Searcher interface{ Search(ctx, req) (*SearchResponse, error) }`）。 |
| 3 | T2（:50 vs :48） | 完成条件写「含解析 `config.example.yaml` 成功」，但 `config.example.yaml` **不在 T2 产出列表**。需补进产出。 |
| 4 | T6（:82） | 「SWRR 权重分布」是**统计断言**，未写样本量/容差，易写成 flaky 测试（如 10 次请求断言精确 50/50）。建议注明「足够样本 + 容差（±5%）」。 |
| 5 | T3/T4（:58/:66） | 「假时钟注入」依赖时钟抽象，但 `BreakerOptions`/`PoolOptions` **未暴露 clock 字段**。同包测试可访问 unexported 字段兜底，但完成条件提了"假时钟"却无对应接口。 |
| 6 | T1（:43）、T9（:102） | 「字段名逐字一致」「测试名可追溯 A 编号」是**人工检查**，非可执行命令。本身合理，但应标注为「人工 gate」，与 `go build/test` 的机器 gate 分开。 |

### 极轻

- T3 完成条件写「`closed→open→half-open→closed` 全转移」，**漏了 `half-open → open`（探测失败）**这一支。这与 A6 自身的遗漏一致（A6 也只写了探测成功），但既然 T3 写"全转移"，就该补失败支——否则熔断器最关键的"半开探测失败回 open"未被测到。

### 结论

- **验收目的：基本能达成**，A1–A10 覆盖完整。
- **但"可执行"有 1 中 + 5 轻**，核心是两个：① 非密闭冒烟混进硬门槛（gate #3/#4、T8）；② T7 的 502/400 因 `NewHandler` 收具体类型而难以注入测试。
- 建议开工前补：拆 gate（自动化/人工分层）、`NewHandler` 接口化、`config.example.yaml` 入产出、SWRR 容差、时钟抽象、T3 补探测失败支。

---

## 十、修订记录（响应 §九 可执行验收审查）

> 修订日期：2026-10-07。

### 中

| 项 | 决策 | 位置 |
|---|---|---|
| 非密闭冒烟混进硬门槛 | 总验收拆两档：自动化门槛（build/vet/test）+ 人工冒烟（真 key 发布前跑）；T8 完成改指向人工冒烟 | `tasks.md` 总验收 + T8 |

### 轻

| 项 | 决策 | 位置 |
|---|---|---|
| T7 502/400 难注入 | `NewHandler` 改收窄接口 `Searcher{Search(ctx,req)}`，`*router.Router` 自然满足，T7 注入 fake | `api-contract.md` server + `tasks.md` T7 |
| T2 config.example.yaml 不在产出 | 补进 T2 产出（作为 Load 解析验证目标） | `tasks.md` T2 |
| SWRR 权重分布无容差 | 注明「足够样本 ≥1000 次 + 容差 ±5%」 | `tasks.md` T6 |
| 假时钟无抽象 | 定「包内 `var now = time.Now`，同包测试覆盖」 | `tasks.md` T3/T4 |
| T1/T9 人工检查未分层 | 归入总验收「人工检查项」注，与机器 gate 分开 | `tasks.md` 总验收 |

### 极轻

| 项 | 决策 | 位置 |
|---|---|---|
| T3 漏 half-open→open | 补「探测失败→open」支，并同步修 A6 | `tasks.md` T3 + `operation.md` A6 |

### 结论

§九 1 中 + 5 轻 + 1 极轻全部收敛。验收门槛分自动化/人工两档，T7 可注入测试，各任务完成条件密闭可执行。

---

## 十一、§十 修复核验

> 复核日期：2026-10-07。逐条核对 §十 声称的 7 项修复是否真实落地、是否引入新不一致。

| # | 项 | 核验 |
|---|---|---|
| 1 | 非密闭冒烟拆两档 | ✓ `tasks.md` 总验收已拆「自动化门槛」（build/vet/test）+「人工冒烟」（curl/QPS）+「人工检查项」注 |
| 2 | NewHandler 接口化 | ✓ `api-contract.md:171-175` 定义 `Searcher` 接口 + `NewHandler(s Searcher)`；`*router.Router.Search` 结构满足；`tasks.md` T7 注入 fake |
| 3 | config.example.yaml 入产出 | ✓ `tasks.md` T2 产出含 `config.example.yaml` |
| 4 | SWRR 容差 | ✓ `tasks.md` T6「足够样本 ≥1000 次 + 容差 ±5%」 |
| 5 | 假时钟抽象 | ✓ `tasks.md` T3/T4「包内 `var now = time.Now`，同包测试覆盖」 |
| 6 | 人工检查分层 | ✓ `tasks.md` 总验收「人工检查项（不阻塞 CI）」注 |
| 7 | T3 补探测失败支 | ✓ `tasks.md` T3「及 `half-open→open`（探测失败）」+ `operation.md` A6 补「探测失败 → open」 |

### 一致性复核

- `Searcher` 接口签名与 `*router.Router.Search` 完全一致（`api-contract.md:157` vs `:171-172`），结构满足，无新悬空。
- T8 的 curl 已从「完成」移除、指向总验收「人工冒烟 #4」，无重复。
- 未发现新引入的不一致。

### 结论

§十 的 7 项修复**全部真实落地**，无新问题。验收门槛已密闭/人工分层，T7 可注入测试。tasks.md 现可作为开工的可靠依据，验收链（A1–A10 + 总验收两档）闭合。
