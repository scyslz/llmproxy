# Requirements Document

## Introduction

在 Virtual Key 鉴权开启时，增加一份**全局 IP 白名单**。名单与单个 Key 无关：来源 IP 命中白名单时，请求可免 Key 进入后续路由；白名单为空时，请求必须携带有效 Virtual Key。系统部署在 Nginx 等反向代理后时，从 `X-Real-IP` / `X-Forwarded-For` 解析真实客户端 IP。

## Glossary

- **Virtual Key**: 客户端密钥（现有实体），开启 `enableVirtualKey` 后用于鉴权与路由绑定。
- **全局 IP 白名单 (Allowed IPs)**: 独立于单个 Key 的 IP / CIDR 列表。命中后该来源可免 Key 访问代理。
- **真实客户端 IP**: 关闭信任反向代理时为 TCP 对端地址；开启后从 `X-Real-IP` / `X-Forwarded-For` 解析，失败时回退 TCP 对端地址。
- **信任反向代理 (Trust Proxy)**: 全局开关。开启后才采信转发头；关闭时只使用 TCP 对端地址，避免直连伪造头。
- **CIDR**: 无类别域间路由表示，例如 `10.0.0.0/8`、`192.168.1.0/24`、`2001:db8::/32`。
- **enableVirtualKey**: 全局开关。关闭时代理保持现有开放访问；开启后执行 Key 鉴权，并叠加全局 IP 白名单规则。

## Requirements

### Requirement 1: 全局 IP 白名单配置

**User Story:** AS 管理员, I want 配置一份与 Key 无关的全局 IP 白名单, so that 可信来源可以免 Key 调用代理。

#### Acceptance Criteria

1. WHEN 管理员在设置中编辑全局 Allowed IPs, the system SHALL 允许填写 IP / CIDR 列表，每项为 IPv4、IPv6 或 CIDR。
2. WHEN 管理员保存 Allowed IPs, the system SHALL 将列表持久化到全局配置。
3. WHEN Allowed IPs 为空或未配置, the system SHALL 在 `enableVirtualKey` 为 true 时要求请求携带有效 Virtual Key。
4. WHEN 管理员查看设置页, the system SHALL 展示当前 Allowed IPs 列表及条目数量。
5. IF 某条 Allowed IP 无法解析为 IP 或 CIDR, the system SHALL 拒绝保存并返回校验错误。

### Requirement 2: Virtual Key 开启后的鉴权顺序

**User Story:** AS 系统, I want 在 Key 鉴权开启时先识别可信 IP, so that 白名单内来源免 Key，其余来源必须使用有效 Key。

#### Acceptance Criteria

1. WHEN `enableVirtualKey` 为 true 且真实客户端 IP 命中全局 Allowed IPs 中任一条（精确 IP 或 CIDR 包含）, the system SHALL 允许该请求通过鉴权；若请求同时携带有效 Virtual Key，the system SHALL 仍按该 Key 做路由绑定。
2. WHEN `enableVirtualKey` 为 true、真实客户端 IP 命中白名单、且请求未携带 Key 或 Key 无法匹配, the system SHALL 放行该请求并使用现有默认路由（全局 enabled providers）。
3. WHEN `enableVirtualKey` 为 true、Allowed IPs 为空, the system SHALL 要求请求携带有效 Virtual Key，匹配失败时返回现有 HTTP 401。
4. WHEN `enableVirtualKey` 为 true、Allowed IPs 非空、且真实客户端 IP 未命中任何条目, the system SHALL 要求请求携带有效 Virtual Key，匹配失败时返回现有 HTTP 401。
5. WHEN `enableVirtualKey` 为 false, the system SHALL 跳过 Virtual Key 校验与 IP 白名单校验。

### Requirement 3: 解析 Nginx / 反向代理后的真实 IP

**User Story:** AS 运维, I want 用开关决定是否采信转发头, so that Nginx 后能拿到真实 IP，直连（含容器）只看 TCP 对端。

#### Acceptance Criteria

1. WHEN `trustProxy` 为 false, the system SHALL 使用 TCP 对端地址（去掉端口）作为真实客户端 IP，并忽略 `X-Real-IP` 与 `X-Forwarded-For`。
2. WHEN `trustProxy` 为 true 且请求携带合法 `X-Real-IP`, the system SHALL 将该值作为真实客户端 IP。
3. WHEN `trustProxy` 为 true、未携带合法 `X-Real-IP` 但携带 `X-Forwarded-For`, the system SHALL 取该头从左到右第一个合法 IP 作为真实客户端 IP。
4. WHEN `trustProxy` 为 true 且转发头均无法得到合法 IP, the system SHALL 使用 TCP 对端地址（去掉端口）作为真实客户端 IP。
5. WHEN 真实客户端 IP 为 IPv4-mapped IPv6（例如 `::ffff:192.168.1.10`）, the system SHALL 按对应 IPv4 地址参与白名单匹配。
6. WHEN 白名单条目为 CIDR, the system SHALL 按网络包含关系匹配真实客户端 IP（IPv4 与 IPv6 分别匹配对应族）。
7. WHEN 管理员查看设置页, the system SHALL 说明：容器 host 网络下 TCP 对端即客户端 IP；bridge NAT 常见为 `172.17.0.1`；同网络内容器互访为对端容器 IP；Nginx 后需开启 Trust Proxy 并由 Nginx 写入 `X-Real-IP`。

### Requirement 4: 管理 API 与配置兼容

**User Story:** AS 管理员, I want 通过现有 Settings API 读写全局白名单, so that 配置可随 config.json 持久化且旧配置可继续加载。

#### Acceptance Criteria

1. WHEN `PUT /api/settings` 请求体包含 `allowedIps` 数组, the system SHALL 将该数组写入全局配置。
2. WHEN `PUT /api/settings` 请求体包含 `trustProxy`, the system SHALL 将该布尔值写入全局配置。
3. WHEN `GET /api/settings` 返回设置, the system SHALL 包含 `allowedIps` 与 `trustProxy` 字段。
4. WHEN 加载既有 config.json 且缺少 `allowedIps` 或 `trustProxy`, the system SHALL 将 `allowedIps` 视为空列表、`trustProxy` 视为 false 并成功启动。
5. WHEN 保存配置, the system SHALL 将 `allowedIps` 与 `trustProxy` 写入 `config/config.json` 的顶层字段。
