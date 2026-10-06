# search-router

一个精简的搜索 API 智能网关：聚合多个 Search API（Serper / Tavily / Brave / …），对外暴露统一接口，根据各 provider 的**配额（quota）**与**限速（QPS/RPM）**自动做负载均衡与**主动分流**。

## 目标

- 统一接口 + 请求/响应归一化
- 按配额 + 限速的**主动**负载均衡（发送前判断，不等出错才切换）
- 限流（令牌桶）、配额记账（日/月/总 + 自动 reset）、熔断、降级
- 单进程后端，无前端 / 无鉴权 / 无 MCP —— 只做内核

## 状态

**设计阶段** —— 尚未写代码。设计文档见 `docs/`，由粗到细：

- [`architecture.md`](docs/architecture.md) —— 模块与依赖朝向
- [`dataflow.md`](docs/dataflow.md) —— 数据流与状态变化
- [`data-model.md`](docs/data-model.md) —— 实体、字段与约束
- [`api-contract.md`](docs/api-contract.md) —— 接口契约（最细）
- [`operation.md`](docs/operation.md) —— 出错处理与降级（横切）

## 参考

- 令牌桶 / 配额 / 熔断算法参考 [SearchHub](https://github.com/woodcoal/SearchHub)（MIT）
- 架构思想参考 [LiteLLM](https://github.com/BerriAI/litellm)
