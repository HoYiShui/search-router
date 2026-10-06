# design-review — 五层设计一致性审查

> 审查日期：2026-10-06。审查对象：`docs/` 下五份设计文档（architecture / dataflow / data-model / api-contract / operation）。
> 审查目标：核对五个粒度层次之间设计的一致性，并判断这五份文档能否直接支撑下一阶段开发任务的拆分。
> 完成度参照：`/Users/hoyishui/Workspace/deep-research-for-Academic/docs/mono/` 下五份文档。

---

## 一、总体结论

五份文档作为**精简单二进制搜索网关**的设计，粒度划分清晰合理：

- `architecture.md` —— 最粗：包划分与依赖朝向
- `dataflow.md` —— 行为：数据流与状态变化
- `data-model.md` —— 实体、字段、关系与约束
- `api-contract.md` —— 最细：签名级接口契约
- `operation.md` —— 横切：出错处理、重试、降级

核心差异化「主动分流」的 `eligibility（资格） vs weight（权重）` 拆解（`dataflow.md` §2）是一个想清楚了的决策，不是半成品。

但对照参考标准，当前文档在**三个维度上不足以直接支撑任务拆分**：

1. 存在跨文档的**硬性不一致**，会直接误导编码（幽灵 provider `Exa`、`MaxKeyAttempts` 未进入控制流、响应示例自相矛盾）。
2. 关键状态机与契约**只写了一半**（key 状态机只有 3 个状态名、1 条转移；adapter 的上游契约完全空白）。
3. **完全没有验收标准**（参考 `operations.md §8` 的 A1–A13 是任务拆分的锚点，当前 `operation.md` 无对应物）。

---

## 二、五层一致性核对结果

### 2.1 硬性不一致（互相矛盾，必须改）

| # | 位置 | 问题 |
|---|---|---|
| **1** | `data-model.md:192` vs `architecture.md:20` vs `README.md:3` | `data-model.md` 写「Tavily / **Exa** → body」，但 architecture 包列表只有 `providers/{serper,tavily,brave}`，README 也是「Serper / Tavily / Brave / …」。**Exa 只在一处出现**，是幽灵 provider。要么补进 architecture + README，要么从 data-model 删掉。 |
| **2** | `operation.md:22` vs `dataflow.md:44-54` | `operation.md` 写「换 key（同 provider，最多 MaxKeyAttempts 次）」，但 dataflow 的 split 伪代码里 key 故障是「`ReportFailure`，回 4（换 key，同 provider）」——**无上界、可无限循环直到 `ErrNoKeyAvailable`**。`MaxKeyAttempts` 约束在控制流里完全没体现。实现者会不知道「换满 N 个 key 后怎么办（是否升级为 provider 故障换 provider）」。 |
| **3** | `api-contract.md:38-48` vs `data-model.md:135` | 响应示例里 `provider:"serper"` 同时 `switchedFrom:"serper"`，且 attempts 只有一条 serper 失败记录。这与「switchedFrom = 最初选中的 provider（降级时才有）」矛盾——serper→serper 不是「换 provider」。若真实故事是「serper 第一个 key 429、第二个 key 成功」，则 `switchedFrom` 应为空、且应体现成功的那次 attempt。示例本身自相矛盾，会误导响应序列化实现。 |

### 2.2 定义缺口（被引用但从未定义）

| # | 位置 | 缺口 |
|---|---|---|
| **4** | `architecture.md:21` vs `data-model.md §配置实体` | architecture 说 config「加载 **yaml/env**」，但 data-model 只给了 yaml tag，**env 映射（哪些变量、优先级、覆盖规则）零定义**。实现 config 包时无据可依。 |
| **5** | `data-model.md:58-72` | 定义了 `KeyState`（active / cooling / quarantined），但全文档**没有 key 状态机图/转移表**。已知转移只有：`keyRateLimited→cooling`（operation:12）、`keyInvalid→quarantined`（operation:11）、`keyQuotaExhausted→quarantined`（operation:13）、`quarantined(配额)→active`（operation:42）。**缺失**：`cooling→active`（RetryAfter 到期后）、`cooling→quarantined`（冷却期再失败）、`keyInvalid 隔离→active`（是否永久/手动）。对比 breaker 状态机（operation:29-36 有完整 mermaid），key 状态机明显只写了一半。 |
| **6** | `api-contract.md §internal` | 定义了 `providers.Provider` 接口，但**每个 adapter 的上游契约完全空白**：serper / tavily / brave 各自的 endpoint、鉴权头、请求参数映射、上游响应字段 → `model.SearchResult` 的翻译规则，一字未提。这是实现 `providers/*` 三个包的直接输入，缺失会导致这三个任务无法拆分。 |
| **7** | `data-model.md:144` | `Attempt.Code` 注释「ErrorKind 字符串，或 `no_key_available` / `circuit_open`」，但这两个**不是 `ErrorKind` 枚举成员**（ErrorKind 只有 5 个）。它们是「attempt 级 code」而非「错误类别」，文档没区分这层，实现时容易把 `no_key_available` 塞进 `ErrorKind` 常量表。 |

### 2.3 冗余 / 歧义（会导致实现走样）

| # | 位置 | 问题 |
|---|---|---|
| **8** | `api-contract.md:56-66` vs `data-model.md:30-31` | `Provider` 接口同时有 `Capabilities()`（软能力，含 `page`）和 `SupportsPaging() bool`。而 dataflow:30 把 `page` 列进软能力、可静默忽略。那么 `SupportsPaging()` 到底算什么——是软能力的冗余别名，还是独立的硬能力？没定义两者关系，实现会纠结「page 到底走哪条判断」。 |
| **9** | `data-model.md:134-135` | `Degraded`=「换过 provider/**key**」，`SwitchedFrom`=「最初选中的 provider」。只换了 key（没换 provider）时，`degraded=true` 但 `switchedFrom` 该置空还是置同名 provider？未定义。结合问题 #3 一起修。 |
| **10** | `data-model.md:113` | `SafeSearch string` 取值未枚举（`off` / `on` / `moderate`？）。api-contract 示例只有 `"off"`。软能力之一却无闭集。 |

---

## 三、对照参考标准的完成度缺口

参考 `docs/mono/` 每份文档都带一套「能直接拆任务」的结构，当前文档对应缺失如下：

| 参考标准要素 | 参考文档位置 | search-router 现状 |
|---|---|---|
| 文档头（版本 + 状态 + 「先读本文」读序） | 每份 mono 文档第 1-4 行 | ✗ 无版本/状态头，读序只在 README |
| 统一术语表 | architecture §1「统一术语」 | ✗ 「eligibility / 降级 / 冷却 / 隔离」散落各处，无集中术语 |
| 枚举闭集表 | data-model §1.1 | △ 枚举分散，且 safeSearch 缺闭集（#10） |
| 状态机图（mermaid） | dataflow / operations 多处 | △ breaker 有、**key 无**（#5） |
| 时序图（mermaid） | dataflow §2 / §6 | ✗ dataflow 只有伪代码，无 sequenceDiagram |
| ER 图 | data-model §7 | △ 只有文字关系，无 erDiagram |
| **验收清单（A1–A13）** | **operations §8** | **✗ 完全缺失** |
| 「非现有能力声明」免责 | 每份 mono 头部 | △ 只在 README「设计阶段」 |

其中**验收清单是最关键的缺口**：它是任务拆分的锚——参考文档用 A1–A13 把每个行为翻译成「要证明什么 + 需要什么证据」，任务才能据此拆解并判定完成。当前 `operation.md` 没有任何「完成 = 什么行为通过什么验证」的表述。

---

## 四、支撑任务拆分的必需补齐项（按优先级）

### P0 —— 不补就无法拆任务

1. **adapter 上游契约**（#6）：为 serper / tavily / brave 各补一节「endpoint / 鉴权 / 请求映射 / 响应→`SearchResult` 翻译」，否则 `providers/*` 三个任务无法定义。
2. **key 状态机完整化**（#5）：补一张与 breaker 同级的 mermaid 状态图 + 转移表，把 cooling / quarantined 的全部出口定义清楚。
3. **验收清单**：新增一节（建议放 `operation.md` 末尾），按「主动分流正确性 / 熔断 / 配额 reset / 错误分类 / 降级 / 502/400 映射」列 A1–A6 级别条目，每条写「行为 + 证据（单测/集成测）」。

### P1 —— 一致性修复（本轮审查的直接产出）

4. 消除 Exa（#1）、让 `MaxKeyAttempts` 进入 dataflow 伪代码（#2）、修正 api-contract 响应示例（#3）。
5. 补 env 配置语义（#4）；厘清 `no_key_available` / `circuit_open` 与 ErrorKind 的分层（#7）。

### P2 —— 打磨

6. 解决 `SupportsPaging` / `Capabilities` 冗余（#8）、`SwitchedFrom` 语义（#9）、safeSearch 闭集（#10）。
7. 补版本头 + 术语表 + mermaid 时序图 / ER 图（对齐参考标准的结构规范）。

---

## 五、修订记录（v0.2，响应本审查）

> 修订日期：2026-10-06。以下逐条对照本审查意见，记录修复位置，供复核。

### P0 —— 补齐缺口

| 审查项 | 修复 | 位置 |
|---|---|---|
| #6 adapter 上游契约空白 | 新增 serper / tavily / brave 的 endpoint、鉴权头、请求映射、响应→`SearchResult` 翻译、contentType（附"实现时对照官方文档"免责） | `api-contract.md`「上游接口」节 |
| #5 key 状态机只写一半 | 新增完整 mermaid `stateDiagram` + 转移表（6 条转移全列），并明确"cooling/quarantined 期间不被选中，故无冷却期再失败转移" | `operation.md`「key 状态机」节 |
| 验收清单缺失 | 新增 A1–A10，每条 = 行为 + 证据（单测/集成测断言） | `operation.md`「验收清单」节 |

### P1 —— 一致性硬伤

| 审查项 | 修复 | 位置 |
|---|---|---|
| #1 幽灵 Exa | 映射改为「Tavily → body；Serper/Brave → abstract」，Exa 消除 | `data-model.md`「约束」 |
| #2 MaxKeyAttempts 未进控制流 | split 伪代码改为内层 `MaxKeyAttempts` 有界循环，满 N 次升级换 provider | `dataflow.md` §2 split 伪代码 |
| #3 响应示例自相矛盾 | 示例改为连贯故事：serper 首 key 429 → 降级 tavily 成功 | `api-contract.md`「外部接口」 |
| #4 env 语义空白 | 定死：env 仅 `SEARCH_ROUTER_CONFIG` 指定 yaml 路径，其余全走 yaml | `architecture.md` 核心决策 + `data-model.md` 配置实体注 |
| #7 code 分层混淆 | 加「attempt code 与 ErrorKind 的分层」，明确 `no_key_available`/`circuit_open` 为本地码、不进 ErrorKind | `data-model.md` |

### P2 —— 打磨

| 审查项 | 修复 | 位置 |
|---|---|---|
| #8 SupportsPaging 冗余 | 删除，翻页并入 `Capabilities()` 的 `"page"` | `api-contract.md` Provider 接口 + `data-model.md` ProviderConfig |
| #9 SwitchedFrom 语义 | 定死：跨 provider 降级才填 `switchedFrom`；只换 key 留空、`degraded=true` | `data-model.md`「约束」 |
| #10 safeSearch 无闭集 | 定义 `off`/`moderate`/`strict` 闭集 + 常量 | `data-model.md` |
| 版本头 / 术语表 / 图 | 五份文档加版本头；architecture 加术语表；dataflow 加 `sequenceDiagram`；data-model 加 `erDiagram` | 全 `docs/` |

### 复核结论

五份文档当前：**硬性不一致已消除、定义缺口已补齐、具备验收锚点（A1–A10）**，可支撑下一阶段 `tasks.md` 拆分。

---

## 六、二轮审查（v0.2 复核）

> 复核日期：2026-10-06。逐条核对 v0.2 修订是否真实落地、是否引入新的不一致。

### 已确认修复正确

| 审查项 | 核验结果 |
|---|---|
| #1 Exa | `data-model.md:225` 已改「Tavily → body；Serper / Brave → abstract」，与 architecture / README 一致 ✓ |
| #2 MaxKeyAttempts | `dataflow.md` §2 伪代码第 4 步已加内层有界循环 ✓ |
| #3 响应示例 | `api-contract.md:26-48` 改为「serper 429 → 降级 tavily 成功」，provider / switchedFrom / attempts 自洽 ✓ |
| #4 env 语义 | architecture / data-model / api-contract / operation 四处一致：「yaml 为主，env 仅 `SEARCH_ROUTER_CONFIG`」 ✓ |
| #5 key 状态机 | `operation.md` §key 状态机 已补 mermaid + 6 条转移表 ✓（但见 R2-1） |
| #6 adapter 契约 | `api-contract.md` §上游接口 已补三 provider 的 endpoint / 鉴权 / 映射 ✓ |
| #7 code 分层 | `data-model.md` §attempt code 与 ErrorKind 的分层 已加两层说明 ✓ |
| #8 SupportsPaging | 已删，并入 `Capabilities()` 的 `"page"` ✓ |
| #9 SwitchedFrom | `data-model.md:226` + `api-contract.md:48` 已定死语义 ✓ |
| #10 safeSearch | `data-model.md` 已加 off / moderate / strict 闭集常量 ✓ |
| 版本头 / 术语 / 图 | 五份文档版本头 ✓、architecture 术语表 ✓、dataflow `sequenceDiagram` ✓、data-model `erDiagram` ✓ |

### 二轮新发现

| # | 级别 | 位置 | 问题 |
|---|---|---|---|
| **R2-1** | **中** | `operation.md:47` | `quarantineMs` 被引用但**全文档未定义**（KeyConfig / ProviderConfig 无此字段、无默认值）。且把 401/403（keyInvalid）设计为「定时后自动回 active」有设计风险：401/403 通常是 key 永久失效/被吊销，定时恢复会反复打一个死 key。建议：keyInvalid 走**永久隔离**（仅手动 reset），或显式定义 `quarantineMs` 并说明理由。 |
| R2-2 | 轻 | `data-model.md:30` vs `dataflow.md:75` vs `operation.md:29` | `MaxKeyAttempts` 口径不一致：「最多换几个 key」vs「最多试 N 次」vs「换满 N 次」——差一（换 N 次 = 试 N+1 个）。应定死为「同 provider 内最多尝试的 key 数（含第一个），缺省 3」。 |
| R2-3 | 轻 | `dataflow.md:70-84` | `MaxKeyAttempts` 因 key 故障耗尽后换 provider，**是否触发 `breaker.RecordFailure()` 未说明**。现状（不触发）对「全 429」是对的，但应写一句「key 级耗尽不记熔断」，避免实现者误加。 |
| R2-4 | 轻 | `api-contract.md:68` | tavily `Page→max_results/offset` 仍标「实现时定」，与「支撑任务拆分」目标不符，应现在定死。 |
| R2-5 | 轻 | `architecture.md:64` vs `dataflow.md:59` | 术语表 eligibility 定义缺「启用」前置条件，dataflow 候选过滤则显式含「启用」。两者对「启用」是静态前置还是动态资格归类不一致。 |
| R2-6 | 轻 | `operation.md:49/73` | 「手动 reset 总配额」无机制（单二进制仅 `POST /search`，无管理端/重载命令）；应明确「= 重启进程清内存态」或补一个 reset 手段。 |

### 二轮结论

五份文档已**消除全部硬性不一致、补齐定义缺口与验收锚点（A1–A10）**，达到「可支撑任务拆分」门槛。剩余 R2-1（`quarantineMs`）建议在拆 `keypool` 任务前先定死；R2-2~R2-6 为轻量口径收敛，可在拆分时随任务写死，不阻塞开工。

---

## 七、二轮修订记录（响应 R2-1~R2-6）

> 修订日期：2026-10-06。

| 审查项 | 修复 | 位置 |
|---|---|---|
| R2-1 `quarantineMs` 未定义 + 401/403 定时恢复风险 | **采纳「永久隔离」方案**：删除 `quarantineMs`（不补定义、直接消除）；keyInvalid（401/403）与总配额耗尽均无自动出口，仅重启进程清内存态恢复 | `operation.md`「key 状态机」+「设计决策」注 |
| R2-2 `MaxKeyAttempts` 口径差一 | 定死「同 provider 内最多尝试的 key 数（含第一个），缺省 3」 | `data-model.md` / `dataflow.md` / `operation.md` 三处 |
| R2-3 key 级耗尽是否记熔断未说明 | 明确「key 级故障耗尽 `MaxKeyAttempts` **不触发** `breaker.RecordFailure()`，只有 `providerUnavailable` 才记」 | `dataflow.md` §2 + `operation.md`「重试与降级」 |
| R2-4 tavily `Page` 映射未定死 | 定死「tavily 无翻页：`page` 不在软能力内、`Page>1` 软忽略；`max_results` 为网关固定值（如 5）」 | `api-contract.md`「上游接口」 |
| R2-5 eligibility 缺「启用」前置 | 术语表新增「启用 enabled」独立项，eligibility 改为「在已启用前提下」 | `architecture.md`「术语」 |
| R2-6 手动 reset 无机制 | 明确「手动 reset = 重启进程清内存态（单二进制无运行时管理端点）」 | `operation.md`「配额与 reset」+「key 状态机」 |

### 二轮结论

R2-1~R2-6 全部收敛，`quarantineMs` 已消除（未定义项以「删除」而非「补定义」处理）。五份文档可支撑 `tasks.md` 拆分，`keypool` 任务的关键决策（keyInvalid/总配额 = 永久隔离）已定死。

---

## 八、三轮复核（终轮）

> 复核日期：2026-10-06。核对 §七 的 R2-1~R2-6 修复是否真实落地、是否引入新矛盾。

### 修复落地核验

| 项 | 核验 |
|---|---|
| R2-1 `quarantineMs` + 永久隔离 | ✓ `operation.md` key 状态机：`quarantineMs` 已删；401/403 与总配额标「永久」；补「设计决策」注 |
| R2-2 MaxKeyAttempts 口径 | ✓ `data-model.md:30` / `operation.md:24` / `dataflow.md:75` 三处统一「最多尝试 N 个 key（含第一个），缺省 3」 |
| R2-3 key 级耗尽不记熔断 | ✓ `operation.md:29` + `dataflow.md:88` 均写「只有 providerUnavailable 才记熔断」 |
| R2-4 tavily 无翻页 | ✓ `api-contract.md:68` 定死「无翻页、Page>1 软忽略、max_results 网关固定值」 |
| R2-5 eligibility 加「启用」 | ✓ `architecture.md:64` 新增「启用 enabled」，`:65` eligibility 改为「在已启用前提下」 |
| R2-6 手动 reset 机制 | ✓ `operation.md:76` + `:56` 定死「= 重启进程清内存态」 |

### 终轮残留（4 处，不阻塞开工）

| # | 级别 | 位置 | 问题 |
|---|---|---|---|
| **R3-1** | **中** | `operation.md:77` | 「因配额耗尽而 quarantined 的 key，到下一边界自动回到 active」是 v0.1 遗留的笼统表述，与「**总配额 = 永久隔离**」矛盾（同文件 line 50/56/76 均写总配额无自动出口）。应改为「因**日/月**配额耗尽」。拆 `keypool` 前须定死，否则总配额耗尽会被错误实现为自动恢复。 |
| R3-2 | 轻 | `operation.md:33-43` vs `:45-52` | key 状态机 mermaid 有 7 条转移（含 `quarantined → active: 重启进程`），转移表只有 6 行，漏「重启进程」出口。 |
| R3-3 | 极轻 | `operation.md:103/106` | A4「换 key 次数 ≤ 上限」与「含第一个共 N 个」差一；A7「到期→active」易误读为 quarantined 到期自动恢复。 |
| R3-4 | 极轻 | `api-contract.md:68` | tavily「page 不在软能力内，Page > 1 软忽略」——不在软能力内则任何 page 都该忽略，「Page > 1」暗示 page=1 被支持。 |

### 终轮结论

三轮审查闭环完成：硬性不一致、定义缺口、冗余歧义均已消除；验收锚点（A1–A10）就位；关键决策（keyInvalid/总配额 = 永久隔离、MaxKeyAttempts = 含第一个共 N 个、key 级耗尽不记熔断）已定死。剩余仅 R3-1（总配额 reset 语义的一处表述矛盾）建议在拆 `keypool` 任务时随任务条目一并收敛，R3-2~R3-4 为表图/措辞级，不阻塞。**五份文档可支撑下一阶段 `tasks.md` 拆分。**

---

## 九、三轮修订记录（响应 R3-1~R3-4）

> 修订日期：2026-10-06。

| 审查项 | 修复 | 位置 |
|---|---|---|
| R3-1 总配额 reset 表述矛盾 | 「因配额耗尽…自动回 active」改为「因**日/月**配额耗尽…到对应 UTC 日/月边界回 active（总配额除外，永久）」 | `operation.md`「配额与 reset」 |
| R3-2 转移表漏「重启进程」出口 | 转移表补第 7 行「quarantined → active：重启进程清态（keyInvalid / 总配额）」，与 mermaid 7 条转移对齐 | `operation.md`「key 状态机」 |
| R3-3 A4/A7 措辞差一 / 歧义 | A4 改「尝试 key 数 ≤ MaxKeyAttempts（含第一个）」；A7 明确「keyInvalid/总配额=永久、日/月配额=到边界回 active、cooling 到期=active」 | `operation.md`「验收清单」 |
| R3-4 tavily page 措辞含混 | 去掉「不在软能力内」的含混表述，直接写「`page>1` 软忽略（记入 `IgnoredParams`）」 | `api-contract.md`「上游接口」 |

### 三轮结论

R3-1~R3-4 全部收敛。三轮审查闭环：五份文档**硬性一致、定义完整、验收锚点（A1–A10）就位、关键决策全部定死**，可进入 `tasks.md` 拆分与实现。
