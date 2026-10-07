# search-router

[![Go](https://img.shields.io/badge/Go-1.27+-00ADD8?logo=go&logoColor=white)](./go.mod)

> 面向 AI Agent 的自托管搜索容灾网关
>
> **One protocol. Multiple search providers. Automatic key rotation and failover.**

search-router 把 Serper、Tavily、Brave、Bright Data 等搜索 API 统一成一个协议，并集中处理 API Key 轮换、限流、配额、供应商故障切换和熔断。

它适合需要稳定联网搜索能力的 **AI Agent、RAG 应用和内部自动化服务**。编译成单二进制、单进程，密钥和调用状态都留在自己的机器上。

## 为什么用 search-router？

单个搜索供应商出问题时，Agent 不应该直接失明：

- 一个 Key 失效或被限流：自动换下一个 Key
- 一个供应商故障或超时：自动切换供应商
- 连续失败：熔断，冷却后半开探测
- 每个 Key 独立设置 QPS、日 / 月 / 总配额
- **主动分流**：发送前就判断配额与限速，饱和的 provider 在发请求前掉出候选，不用等 429 才切换
- 单二进制、单进程、无运行时依赖，密钥不离开自己的机器

## 核心概念

路由决策把「资格（eligibility，动态）」和「权重（weight，静态）」拆开：

- **资格**：provider *现在*能不能用 = 启用 + 熔断未开 + 至少一个 Key 有令牌且配额未满 + 满足硬能力。这是「主动」的来源。
- **权重**：在有资格的 provider 之间分多少流量。

两种模式：

| 模式 | 行为 |
|---|---|
| `split`（缺省） | 平滑加权轮询（SWRR），按 `weight` 分流；低并发下自然退化为「首选最高权重」 |
| `failover` | 按 `priority` 排序串行试，饱和才换下一家 |

## 30 秒启动

```bash
go build -o search-router ./cmd/search-router
./search-router
```

看到日志即启动成功：

```text
search-router listening on :8080 (mode=split, providers=4)
```

发一次搜索：

```bash
curl -X POST http://localhost:8080/search \
  -H 'Content-Type: application/json' \
  -d '{"query":"insider threat detection CERT r4.2 to r6.2 transfer learning"}'
```

默认读 `./config.yaml`；换路径用环境变量 `SEARCH_ROUTER_CONFIG`：

```bash
SEARCH_ROUTER_CONFIG=/path/to/config.yaml ./search-router
```

## 第一次配置

```bash
cp config.example.yaml config.yaml
```

编辑 `config.yaml`，把各 provider 的 `value` 换成真实 API Key。`config.yaml` 已在 `.gitignore` 中，不会进 git。

```yaml
routing:
  mode: split          # split | failover

providers:
  - id: serper
    enabled: true
    weight: 1          # split 流量份额
    priority: 1        # failover 排序
    timeout: 10s
    maxKeyAttempts: 3  # 同 provider 内最多尝试几个 Key（含第一个）
    contentType: abstract
    capabilities: [page, country, lang, timeRange]
    keys:
      - id: serper-k1
        value: "<你的 Serper API key>"
        enabled: true
        qps: 5
        totalQuota: 2500
```

字段缺省与校验：

- `weight` 缺省 `1`、`priority` 缺省 `99`、`maxKeyAttempts` 缺省 `3`、`mode` 缺省 `split`
- `qps` 必须 `> 0`；`mode` ∈ `{split, failover}`；`contentType` ∈ `{abstract, body}`
- `dailyQuota` / `monthlyQuota` / `totalQuota` 均可省略或置 `null`（表示不限）

## HTTP API

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

响应体（`model.SearchResponse`）：

```jsonc
{
  "results": [
    { "title": "…", "url": "https://…", "content": "…", "contentType": "body", "score": 0.87, "publishedDate": "2024-03-05" }
  ],
  "meta": {
    "provider": "tavily",        // 实际回答的 provider
    "keyId": "tavily-k2",        // 实际使用的 Key
    "tookMs": 812,
    "degraded": true,            // 是否换过 Key 或 provider
    "switchedFrom": "serper",    // 最初选中的 provider；仅跨 provider 降级时填充
    "ignoredParams": ["timeRange"], // 被 provider 忽略的软参数
    "attempts": [
      { "provider": "serper", "keyId": "serper-k1", "ok": false, "code": "keyRateLimited", "message": "429", "tookMs": 5 },
      { "provider": "tavily", "keyId": "tavily-k2", "ok": true, "tookMs": 807 }
    ]
  }
}
```

`meta.attempts` 会记录本次请求尝试过的 Key 和供应商，便于排障和观察降级。

HTTP 错误码：

| 状态码 | 含义 |
|---|---|
| `200` | 成功，`results` 非空 |
| `400` | 参数错误（缺 `query` / JSON 非法 / 上游 400） |
| `502` | 所有 provider 都失败（`AllProvidersFailedError`） |

## 内置供应商

| ID | 定位 | contentType | 主要能力 |
|---|---|---|---|
| `serper` | Google SERP | `abstract` | page、country、lang、timeRange |
| `tavily` | AI 搜索（全文） | `body` | timeRange（无翻页，`page>1` 软忽略） |
| `brave` | Brave Search | `abstract` | page、country、safeSearch |
| `brightdata` | Bright Data SERP（代理抓取） | `abstract` | page、country、lang、timeRange（需配置 `zone`） |

`content: body` 会**硬过滤**掉 `contentType: abstract` 的 provider——降级了就不是全文了。新增供应商只需实现 `providers.Provider` 接口（`ID` / `Search` / `Capabilities` / `ContentType`）并在 `cmd/search-router/main.go` 注册。

## 容灾与配额

| 故障 | 处理 |
|---|---|
| Key 无效（401/403） | 永久隔离该 Key，切换下一个 Key |
| Key 限流（429） | 按 `Retry-After` 冷却，切换下一个 Key |
| 配额耗尽（日 / 月） | 隔离到对应 UTC 边界，切换下一个 Key |
| 配额耗尽（总） | 永久隔离，切换下一个 Key |
| 供应商故障（5xx / 超时 / 网络错） | 不惩罚 Key，累计熔断计数并切换供应商 |
| 参数错误（400） | 不重试，直接返回 400 |

每个 provider 和每把 Key 都可以设置：

- QPS（令牌桶）
- 日配额 / 月配额 / 总配额
- 优先级 / 权重 / 超时
- 单供应商最大换 Key 次数（`maxKeyAttempts`）

熔断状态机 `closed → open → half-open → closed`，连续失败 ≥ 阈值（缺省 3）即 open，冷却期满（缺省 5m）后半开探测。key 状态机 `active / cooling / quarantined`。

## 数据与安全

- 运行时状态（令牌桶 / 配额计数 / 冷却 / 熔断）**全在内存**，重启清零——单二进制无管理端点，恢复 = 重启进程。
- `config.yaml`（含真实 Key）已入 `.gitignore`，仓库只跟踪占位 Key 的 `config.example.yaml`。
- 配额计数不持久化：重启后本地计数清零，会靠上游 429 / 402 重新学会「哪个 Key 已耗尽」（可选 SQLite 持久化，暂未做）。
- 多实例部署需把内存态换成 Redis（暂不做）。

## 从源码开发

```bash
go build ./...   # 编译
go test ./...    # 单元 + 集成测试（验收清单 A1–A10）
go vet ./...     # 静态检查
```

测试覆盖 `operation.md` 的验收清单 A1–A10（权重分布、资格过滤、硬能力过滤、换 Key 上界、降级、熔断状态机、key 状态机、配额 reset、502/400 映射、adapter 归一化）。

## 文档

| 文档 | 内容 |
|---|---|
| [docs/architecture.md](./docs/architecture.md) | 模块与依赖朝向、包划分 |
| [docs/dataflow.md](./docs/dataflow.md) | 数据流、路由决策、状态变化 |
| [docs/data-model.md](./docs/data-model.md) | 实体、字段、关系与约束 |
| [docs/api-contract.md](./docs/api-contract.md) | 接口契约（外部 / 上游 / 内部，签名级） |
| [docs/operation.md](./docs/operation.md) | 出错处理、重试、降级、验收清单 A1–A10 |
| [docs/tasks.md](./docs/tasks.md) | 开发任务拆分（T0–T9）+ 总验收 gate |
| [docs/design-review.md](./docs/design-review.md) | 设计三轮审查与修订轨迹 |
| [docs/task-review.md](./docs/task-review.md) | 任务编排 + 可执行验收审查 |
| [docs/code-review.md](./docs/code-review.md) | 实现代码审查与修订记录 |

## 参考与许可

令牌桶 / 配额 / 熔断 / 错误分类的算法思路参考 [SearchHub](https://github.com/woodcoal/SearchHub)（MIT），**代码用 Go 独立重写**（不 fork、不依赖其运行时）；架构思想参考 [LiteLLM](https://github.com/BerriAI/litellm)。

第三方搜索服务的使用仍需遵守各自的服务条款和计费规则。
