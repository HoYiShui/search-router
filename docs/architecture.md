# architecture — 模块与依赖

> v0.2 · 2026-10-06（含 design-review 修订）· 读序：architecture → dataflow → data-model → api-contract → operation

一个精简的搜索 API 智能网关（Go，单二进制）。本文是文档体系里**最粗粒度**的一层：只讲「有哪些包」和「依赖朝向」，不讲行为细节（行为见 `dataflow.md`）。

## 技术形态

- 语言 Go，编译为**单二进制**，无运行时依赖。
- **单进程**：运行时状态全在内存，天然一致（多实例需换 Redis，暂不做）。
- 只做内核：无前端 / 无鉴权 / 无 MCP。
- 监听地址：`:8080`（单二进制默认端口）。
- 共享内存态（令牌桶 / 配额 / 熔断）的原子性用 `sync.Mutex` 保证；超时/取消用 `context.Context` 贯穿。

## 包划分

| 包 | 职责 |
|---|---|
| `server` | 唯一入口 `POST /search`：解析请求、调用 router、返回统一响应 |
| `router` | 跨 provider 主动分流：资格 + 权重选出目标 provider（**核心差异化**） |
| `keypool` | 单个 provider 的 key 池：轮询 + 令牌桶(QPS) + 配额(日/月/总) + 冷却状态机 |
| `breaker` | provider 级熔断 |
| `providers/{serper,tavily,brave}` | 各家 API 的 adapter：实现 `Provider` 接口，翻译成统一 schema |
| `config` | 加载 yaml 到 `model.Config`（env 仅指定配置文件路径） |
| `model` | 共享类型 + 错误定义（被所有包依赖，无环） |

目录：

```
search-router/
├── go.mod                     # module search-router（可改）
├── cmd/search-router/main.go  # 组装依赖、加载配置、启动
└── internal/
    ├── server/
    ├── router/
    ├── keypool/
    ├── breaker/
    ├── providers/{serper,tavily,brave}/
    ├── config/
    └── model/
```

## 依赖朝向

规则：**上层依赖下层，下层不依赖上层；核心逻辑不依赖传输层。**

```
server ──→ router ──→ keypool
   │         │    └──→ breaker
   │         └───────→ providers
   │
   └───────→ model ←──── config / keypool / breaker / providers
```

- `server` 只依赖 `router` 和 `model`，不碰 keypool / 熔断细节。
- `router` 依赖 `keypool` / `breaker` / `providers`，但**反过来不行**。
- `keypool` / `breaker` / `providers` / `config` 是叶子：只依赖 `model`。
- `model` 被所有人依赖，不依赖任何人（无环）。
- `main`（`cmd/search-router`）是唯一的组装根：加载 config → 构造 adapter/pool/breaker → 喂给 router → 交给 server。

## 术语

| 术语 | 含义 |
|---|---|
| 启用 enabled | 配置开关；false 直接排除（静态前置） |
| 资格 eligibility | provider 当前可用的动态判定（在已启用前提下）：熔断未开 + 有 key 可用 + 满足硬能力 |
| 权重 weight | 有资格 provider 之间的流量份额（静态配置） |
| 软能力 / 硬能力 | 可降级参数（忽略并记入 ignoredParams）/ 不可降级需求（排除 provider） |
| 降级 degraded | 首次选择未成功，换 key 或换 provider 后才成功 |
| 冷却 cooling | key 因 429 暂时不可用，到期恢复 |
| 隔离 quarantined | key 因 401/403 或配额耗尽而不可用 |
| 熔断 | provider 级连续失败保护（closed/open/half-open） |
| contentType | 结果的正文形态：abstract（摘要）/ body（全文） |
| 令牌桶 | 按 QPS 速率放令牌、发请求扣令牌的限速器 |
| 配额 quota | 日/月/总三档用量上限 |

## 核心决策

- 差异化只在 `router` 的「主动分流」；令牌桶 / 配额 / 熔断 / 错误分类参考 SearchHub（MIT）——**算法思路复用，代码用 Go 重写**（不 fork、不依赖其运行时）。
- 单进程是正确默认值：吞吐瓶颈在上游限速，不是本进程（详见 `dataflow.md` §3）。
- 配置：yaml 为主；env 仅 `SEARCH_ROUTER_CONFIG` 指定 yaml 路径（缺省 `./config.yaml`）。
