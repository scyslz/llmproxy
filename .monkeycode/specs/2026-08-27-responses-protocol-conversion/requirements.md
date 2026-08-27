# Requirements Document

Feature: responses-protocol-conversion
Updated: 2026-08-27

## Introduction

LLM Proxy 当前仅支持 OpenAI Chat Completions 协议（`/v1/chat/completions`）的透明转发。本特性为代理增加 OpenAI Responses API（`/v1/responses`）协议支持，并实现三种链路的自动协议转换：

1. Responses 请求 → Chat Completions 上游（responses → chat）
2. Chat Completions 请求 → Responses 上游（chat → responses）
3. 同协议直通（responses → responses、chat → chat）

Provider 在 Web 管理面板中可指定协议类型，代理在请求进入时依据客户端调用的端点确定"入站协议"，依据目标 Provider 的协议类型确定"出站协议"，两者不一致时执行请求体转换与响应回转（含流式 SSE）。

## Glossary

- **入站协议**: 客户端调用代理所用的 API 协议，由请求路径决定（`/v1/chat/completions` = chat，`/v1/responses` = responses）。
- **出站协议**: 目标 Provider 声明的上游协议类型，由 Provider 配置的 `protocol` 字段决定。
- **Chat Completions 协议**: OpenAI `/v1/chat/completions` 接口格式（messages 数组、choices、`prompt_tokens/completion_tokens`）。
- **Responses 协议**: OpenAI `/v1/responses` 接口格式（input 内容项数组、output 数组、`input_tokens/output_tokens`、SSE 事件流 `response.output_text.delta` 等）。
- **协议转换器**: 将一种协议的请求体映射为另一种协议请求体，并将上游响应回转为入站协议格式的组件。
- **直通**: 入站协议与出站协议一致时，沿用现有透明转发路径。
- **Per-model 协议覆盖**: 以 (provider, model) 粒度记录的实际协议类型，优先级高于 Provider 默认 `protocol`，由自动探测写入并持久化。

## Requirements

### Requirement 1 — Provider 协议类型配置

**User Story:** AS 管理员, I want 在 Provider 设置中指定其上游协议类型, so that 代理知道如何与该 Provider 通信并在需要时自动转换协议。

#### Acceptance Criteria

1. THE System SHALL 为每个 Provider 提供 `protocol` 配置字段，取值为 `chat` 或 `responses`。
2. WHEN Provider 配置未指定 `protocol`，THE System SHALL 将其默认为 `chat`，保持与现有行为兼容。
3. WHEN 管理员在 Provider 编辑表单中选择协议类型并保存，THE System SHALL 持久化 `protocol` 到 config.json 并立即生效。
4. IF `protocol` 取值超出 `{chat, responses}` 集合，THE System SHALL 在启动校验与保存接口中拒绝该配置并返回错误信息。
5. THE System SHALL 为每个 Provider 维护 `modelProtocols` 映射（model 名 → 协议类型），用于记录探测发现的 per-model 实际协议。

### Requirement 1a — Per-model 协议自动探测

**User Story:** AS 管理员, I want 代理在 Provider 配置协议与某模型实际支持协议不一致时自动探测并记住正确协议, so that 同一 Provider 下不同模型可各用其合适的协议且人工无需逐一核对。

#### Acceptance Criteria

1. THE System SHALL 在确定出站协议时优先读取 `modelProtocols[请求模型]`，缺失时回退 Provider 默认 `protocol`。
2. WHEN 按当前协议请求上游返回 HTTP 404（或明确表示路径/端点不存在的状态）且入站为对话补全类 POST，THE System SHALL 以另一协议对同一 (provider, model) 重试一次，每个候选 provider 单次请求至多触发一次探测重试。
3. WHEN 探测重试成功返回 2xx，THE System SHALL 将实际使用的协议写入 `modelProtocols[model]` 并持久化到 config.json，后续同模型请求直接使用该协议。
4. WHEN 管理员通过创建或更新接口保存 Provider，THE System SHALL 清空该 Provider 的 `modelProtocols` 字段，等待下一次探测重新写入。
5. WHEN 探测重试也失败，THE System SHALL 保持原降级链行为并继续尝试下一候选 provider，`modelProtocols` 保持不变。
6. THE System SHALL 在系统日志中记录探测触发的请求标识、provider、model 与新旧协议，便于排查。

### Requirement 2 — Responses 端点接入

**User Story:** AS 使用 Responses SDK 的客户端, I want 通过代理的 `/v1/responses` 端点发起对话请求, so that 无需修改客户端代码即可使用代理的多 Provider 路由能力。

#### Acceptance Criteria

1. THE System SHALL 在 `/v1/responses` 路径上接收 POST 请求，并复用现有的虚拟密钥校验、候选链选择、熔断、并发控制与降级逻辑。
2. THE System SHALL 从 `/v1/responses` 请求体中解析 `model` 字段用于模型替换与用量统计。
3. THE System SHALL 对 `/v1/responses` 的成功响应按现有逻辑统计 usage 与 model 并写入请求日志，`path` 记录实际入站路径。

### Requirement 3 — responses → chat 协议转换

**User Story:** AS 调用 `/v1/responses` 的客户端, I want 请求被自动转换为 Chat Completions 并发往仅支持 chat 协议的上游, so that 我可以使用 Responses 接口访问任何 chat 兼容 Provider。

#### Acceptance Criteria

1. WHEN 入站协议为 responses 且目标 Provider 协议为 chat，THE System SHALL 将 Responses 请求体的 `input` 内容项（含 system/user/assistant 角色文本）映射为 Chat 请求体的 `messages` 数组，其余参数做兼容映射（如 `temperature`、`max_output_tokens`→`max_tokens`、`top_p`、`stream`），并删除 chat 不识别的 Responses 专有字段（如 `instructions` 合并为 system message 后移除原字段）。
2. WHEN 上游返回 chat 非流式响应，THE System SHALL 将其回转为 Responses 格式响应：`output` 数组包含 `message` 类型内容项、`status: "completed"`、usage 映射为 `input_tokens/output_tokens/total_tokens`、保留 `id`/`model`/`created` 字段。
3. WHEN 上游返回 chat SSE 流式响应且入站请求 `stream=true`，THE System SHALL 将 chat 流增量（`delta.content`、finish_reason、usage chunk）实时转译为 Responses 流事件序列（至少覆盖 `response.created`、`response.output_item.added`、`response.output_text.delta`、`response.completed`），并以 `text/event-stream` 回传给客户端。
4. WHEN 上游 chat 响应包含 `tool_calls`，THE System SHALL 将其映射为 Responses `function_call` 输出项；入站 Responses 请求中的 function tools 定义 SHALL 映射为 chat `tools` 定义。

### Requirement 4 — chat → responses 协议转换

**User Story:** AS 调用 `/v1/chat/completions` 的客户端, I want 请求被自动转换为 Responses API 并发往仅支持 responses 协议的上游, so that 标准聊天客户端也能使用 Responses 型 Provider。

#### Acceptance Criteria

1. WHEN 入站协议为 chat 且目标 Provider 协议为 responses，THE System SHALL 将 chat 请求体的 `messages` 数组映射为 Responses `input` 内容项数组，参数做兼容映射（如 `max_tokens`→`max_output_tokens`、`temperature`、`top_p`、`stream`）。
2. WHEN 上游返回 responses 非流式响应，THE System SHALL 将其回转为 Chat 格式响应：`choices[0].message` 含角色与拼接后的输出文本，`finish_reason` 按 `status` 推导（completed→stop、incomplete→length），usage 映射为 `prompt_tokens/completion_tokens`，保留 `id`/`model`/`created` 字段。
3. WHEN 上游返回 responses SSE 流式响应且入站请求 `stream=true`，THE System SHALL 将 responses 流事件中的文本增量聚合转译为 chat 流 chunk 序列（含首个带 role 的 chunk、`delta.content` 增量 chunk、终止 finish_reason chunk 及 `[DONE]`）。
4. WHEN 上游 responses 输出包含 `function_call` 项，THE System SHALL 将其映射为 chat `message.tool_calls` 结构。

### Requirement 5 — 直通与非流式降级

**User Story:** AS 管理员, I want 同协议请求保持现有透明转发行为, so that 新逻辑对既有配置零影响。

#### Acceptance Criteria

1. WHEN 入站协议与出站协议一致（chat→chat 或 responses→responses），THE System SHALL 沿用现有透明转发路径，逐字节透传请求体与响应体。
2. WHEN 入站请求未声明 `stream:true` 但上游返回流式响应，THE System SHALL 先完成流式聚合再回转出站协议的非流式响应结构。
3. THE System SHALL 保证无 `protocol` 字段的存量配置文件加载后行为与升级前完全一致。

### Requirement 6 — Playground 兼容

**User Story:** AS 管理员, I want Web Playground 继续对新旧协议 Provider 可用, so that 配置后可以即时验证连通性。

#### Acceptance Criteria

1. WHEN Playground 选择 responses 协议的 Provider 发起测试对话，THE System SHALL 以 chat 协议构造请求并依赖服务端转换链路获得结果。
2. THE System SHALL 保持 Playgrouund 现有 direct-chat 接口对 chat 协议 Provider 行为不变。
