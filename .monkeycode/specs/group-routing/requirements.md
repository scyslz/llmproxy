# Requirements Document

## Introduction

在 LLM Proxy 中引入 **Group（Provider 组）** 管理实体，将原先"key 直接绑定多个 provider 并按优先级降级"的能力迁移到 Group 层。Key 绑定 Group，Group 内按顺序执行 provider 直到成功，全部失败返回 502。采用"冷却判断 + 真实请求即探测"的懒探测机制：请求到来时检查 provider/model 冷却状态，冷却期内跳过，冷却期过后以真实请求探测可用性，失败按 30s/60s/90s 累加冷却（最长 10 分钟），成功即恢复。管理界面新增 Groups tab，支持 Group CRUD、一键测试（成功/失败/耗时）。

## Glossary

- **Provider**: 上游 LLM 服务提供商（现有实体）。
- **Group**: 一组按优先级排序的 Provider 集合（新实体），由用户创建并配置。
- **Virtual Key**: 客户端密钥（现有实体），从"绑定多个 provider"迁移为"绑定一个 Group"，providerIds 保留为 fallback。
- **Cooling（冷却）**: provider/model 转发失败后进入的不可用状态，按 30s、60s、90s……累加的不可用时长，上限 10 分钟。
- **Lazy Probing（懒探测）**: 请求到来时检查冷却状态，冷却期内跳过，冷却期过后以真实转发请求探测可用性的机制。
- **Model 可用性**: provider 上某个 model 是否可被成功调用（响应非错误状态码）。

## Requirements

### Requirement 1: Group 实体与管理

**User Story:** AS 管理员, I want 创建并管理 Provider 组, so that 一组 provider 可以作为一个整体被 key 引用。

#### Acceptance Criteria

1. WHEN 管理员在 Groups tab 创建组, the system SHALL 生成带唯一 ID 的 Group，支持填写组名、选择有序 provider 列表。
2. WHEN 管理员编辑 Group, the system SHALL 允许调整 provider 顺序（优先级）、增删 provider。
3. WHEN 管理员删除 Group, the system SHALL 解除被引用 key 的绑定并将该 key 置为无效引用。
4. IF Group 引用了不存在的 provider, the system SHALL 在列表中标记该 provider 为"缺失"并跳过其执行。
5. WHEN 管理界面加载, the system SHALL 展示所有 Group，包括组名、provider 数量、各 provider 健康状态。

### Requirement 2: Key 绑定 Group

**User Story:** AS 管理员, I want key 绑定 Group, so that 客户端流量按组内优先级自动路由与降级。

#### Acceptance Criteria

1. WHEN 创建或编辑 key, the system SHALL 允许选择绑定一个 Group（新增 groupId 字段）。
2. WHEN key 配置了 groupId, the system SHALL 使用该 Group 内按顺序排列的 provider 作为转发候选链。
3. IF key 未配置 groupId 但存在 providerIds, the system SHALL 向后兼容：使用 providerIds 数组作为候选链。
4. IF key 既未配置 groupId 也无 providerIds, the system SHALL 使用全局 enabled providers 作为候选链。
5. WHEN key 绑定 Group 后, the system SHALL 在 key 列表中显示组名而非 provider 列表。
6. WHEN key 的 groupId 引用的 Group 不存在, the system SHALL 回退到 providerIds / 全局 enabled 路由。

### Requirement 3: Group 顺序执行与降级

**User Story:** AS 使用者, I want 请求按组内顺序自动降级, so that 单个 provider 故障时仍能获得服务。

#### Acceptance Criteria

1. WHEN 请求到达且 key 绑定 Group, the system SHALL 按 Group 内 provider 顺序逐一尝试转发，直到成功。
2. WHEN Group 内所有 provider 均失败, the system SHALL 返回 HTTP 502 并在响应中包含最后失败 provider 的错误信息。
3. WHEN 单个 provider 在组内失败, the system SHALL 记录一条正常的请求日志（含 provider 名、实际状态码、错误），并继续下一个 provider。
4. WHEN 请求 model 与某 provider 支持列表不匹配, the system SHALL 使用该 provider 的默认模型替换并继续，后续 provider 仍使用用户原始 model 判断。
5. IF 请求 model 在 Group 内某 provider 上处于健康检查不可用状态, the system SHALL 跳过该 provider 的该 model 转发尝试并记录跳过原因。
6. WHEN 健康检查标记某 provider/model 不可用, the system SHALL 在转发时使用该标记跳过对应 provider 上该 model 的尝试。

### Requirement 4: 懒探测健康状态（冷却判断 + 真实请求即探测）

**User Story:** AS 系统, I want 在请求到来时基于冷却状态路由, so that 故障 model 被自动隔离并按时长退避，无需独立后台探测任务。

#### Acceptance Criteria

1. WHEN 请求需要 Group 内某 provider/model 转发, the system SHALL 先检查该 provider/model 的冷却状态。
2. WHEN 该 provider/model 处于冷却期内, the system SHALL 跳过该 provider 该 model 的转发尝试并记录跳过原因，继续下一个候选。
3. WHEN 该 provider/model 冷却期已过或从未记录, the system SHALL 允许转发尝试（首次尝试即探测）。
4. WHEN 真实转发请求成功, the system SHALL 将该 provider/model 标记为可用并清零连续失败计数。
5. WHEN 真实转发请求失败, the system SHALL 将该 provider/model 标记为不可用，冷却时长按 30s、60s、90s 累加，最长 10 分钟。
6. WHEN 冷却期结束后该 provider/model 再次被请求, the system SHALL 自动放行转发尝试（视为恢复探测）。
7. WHEN 冷却状态发生变化, the system SHALL 记录系统日志并在管理界面反映。
8. WHEN 同 provider 不同 model 状态不同, the system SHALL 分别维护各 model 的冷却状态，互不影响。

### Requirement 5: 健康检查探测方式（model 级真实请求即探测）

**User Story:** AS 系统, I want 用真实请求作为探测, so that model 级可用性被准确识别且不引入额外探测流量。

#### Acceptance Criteria

1. WHEN 请求到达且某 provider/model 冷却期已过, the system SHALL 将真实转发请求本身作为探测：成功即恢复可用，失败即进入冷却。
2. WHEN 转发请求成功返回 2xx, the system SHALL 判定该 provider/model 可用。
3. WHEN 转发请求超时或返回非 2xx, the system SHALL 判定该 provider/model 不可用。
4. IF provider 未配置任何 model, the system SHALL 在 Group 路由时跳过该 provider（无可用 model）。
5. WHEN 冷却期内的 provider/model 被跳过, the system SHALL 不发出任何探测流量。
6. IF 同一 provider 的多个 model 存在, the system SHALL 逐个 model 独立维护冷却状态。
7. WHEN 一键测试触发, the system SHALL 对组内每个 provider/model 发起真实 chat 探测请求并返回结果。

### Requirement 6: Group Model 一键测试

**User Story:** AS 管理员, I want 在管理界面一键测试 Group/model, so that 快速判断可用性与耗时。

#### Acceptance Criteria

1. WHEN 管理员点击 Group 的测试按钮, the system SHALL 依次对组内每个 provider/model 发起真实 chat 探测请求。
2. WHEN 测试完成, the system SHALL 展示每个 provider/model 的结果：成功/失败、状态码、耗时（毫秒）。
3. WHEN 测试请求失败, the system SHALL 展示失败原因（连接错误、超时、HTTP 错误）。
4. WHEN 测试进行中, the system SHALL 展示加载状态并禁止重复触发。
5. WHEN 一键测试完成, the system SHALL 更新该 Group 的健康检查标记。

### Requirement 7: 兼容性与迁移

**User Story:** AS 现有用户, I want 现有 key-provider 配置平滑迁移, so that 升级不影响已有客户端。

#### Acceptance Criteria

1. WHEN 升级且 key 仍使用 providerIds, the system SHALL 保持原有按顺序降级行为不变。
2. WHEN 管理员在 Groups tab 创建 Group, the system SHALL 提供"从现有 key 导入 provider 顺序"的操作。
3. WHEN Group 被删除且 key 未绑定新组, the system SHALL 回退到 providerIds 或全局 enabled 路由。
