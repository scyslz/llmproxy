# LLM Proxy

轻量级 LLM API 代理服务，提供 OpenAI 兼容接口和 Web 管理面板。支持多级代理串联，自动传播超时和连接断开信号。编译为单文件 Go 二进制，内置 SQLite 日志存储。

> **English**: A lightweight LLM API proxy with OpenAI-compatible `/v1/*` endpoints and a web dashboard. Single Go binary (embedded React + SQLite), multi-provider fallback, virtual keys (`sk-proxy-*`), SSE streaming, timeout/circuit-breaker, and automatic **Chat ↔ Responses** protocol conversion with per-model auto-probe. See *Configuration* and *Deployment* below (Chinese docs) — quick start: `cp config.json.example config/config.json && npm run build:go && ./dist/llmproxy` → `http://localhost:3000`.

## 核心功能

- **OpenAI 兼容代理** — 透明转发 `/v1/*` 到后端 LLM（chat/completions、embeddings 等）
- **流式传输** — 完整支持 SSE 流式响应，逐级转发，usage/model 增量解析
- **多提供商管理** — 同时配置多个提供商，请求按候选链自动降级
- **虚拟密钥** — `sk-proxy-*` 密钥体系，按 provider 授权，细粒度控制客户端权限
- **模型回退** — 请求模型不在 provider 的 `models` 列表时，自动替换为 `defaultModel`
- **熔断与并发控制** — provider 级别熔断（cooldown 降级）与信号量并发限制
- **可配置上游超时** — 每个 provider 独立设置超时，超时自动向下游返回 504 并断开链路
- **链路断开传播** — 链路任意节点断开，自动向两端传播断开信号，防止连接挂死
- **Web 管理面板** — React SPA，管理提供商/密钥、实时日志、API Playground、用量统计
- **SQLite 日志存储** — 系统日志 + 请求用量日志，支持按 key/model/provider/status/时间过滤
- **hasDetail 实时解析** — 请求日志的 Detail 按钮基于系统日志实时查询，清空日志后自动隐藏
- **可选管理认证** — 管理后台密码保护
- **单文件二进制** — Go 编译，内嵌前端，无 Node/Python 运行时依赖

## 快速开始

```bash
git clone <repo> && cd llmproxy
cp config.json.example config/config.json
# 编辑 config/config.json
npm run build:go && ./dist/llmproxy
```

访问 `http://localhost:3000` 打开管理面板。

## 部署

```bash
# 生产构建（前端 + Go 二进制）
npm run build:go

# 直接启动
./dist/llmproxy

# Docker
docker build -t llmproxy .
docker run -d -p 3000:3000 \
  -v $PWD/config:/app/config \
  -v $PWD/logs:/app/logs \
  llmproxy
```

> **重要**：请求用量数据（SQLite）存放在 `/app/logs/requests.db`，系统日志在 `/app/logs/system_logs.db`。`docker run` 时必须挂载 `logs` 目录，否则容器删除/重建后数据会丢失。配置同样需要挂载（`-v $PWD/config:/app/config`）。

Docker Compose 直接使用镜像 `scyslz/llmproxy`，无需本地构建。容器内的配置目录为 `/app/config`（挂载宿主机的 `config/`），日志与 SQLite 数据位于 `/app/logs`，`compose.yaml` 已自动将这两个目录挂载到宿主机，配置修改后重启生效：`docker compose restart`。

## 配置

```jsonc
{
  "listen": ":3000",
  "enableVirtualKey": false,
  "enableAdminAuth": true,
  "adminPassword": "your-password",
  "debug": false,
  "logDetail": "basic",          // off | basic | error | all
  "logBody": false,              // 是否记录请求/响应体
  "maxLogSizeMB": 10,            // 系统日志库文件大小上限
  "maxRequestLogs": 10000,       // 请求用量日志保留条数，超出自动清理最旧记录
  "activeLogFile": 1,            // 日志桶编号（1|2），轮转时切换
  "providers": [
    {
      "id": "my-provider",
      "name": "My Provider",
      "baseUrl": "https://api.example.com/v1",
      "apiKey": "sk-xxx",
      "enabled": true,
      "models": ["gpt-4o"],
      "defaultModel": "gpt-4o",   // 请求模型不在 models 列表时的回退模型，留空用第一个
      "concurrency": 0,           // 0 = 不限并发
      "timeout": 120000,          // 上游请求超时(ms)，0 或省略 = 不超时
      "openaiEndpoint": ""        // 可选，上游转发路径，如 /chat/completions
    }
  ],
  "keys": [
    {
      "key": "sk-proxy-xxx",
      "name": "all",
      "providerIds": ["all"]     // 绑定具体 provider id 数组，或 ["all"] 走全局启用集
    }
  ]
}
```

### 顶层字段

| 字段 | 类型 | 说明 |
|------|------|------|
| `listen` | string | 监听地址，默认 `:3000` |
| `enableVirtualKey` | boolean | 是否启用虚拟密钥校验 |
| `enableAdminAuth` | boolean | 管理后台密码保护 |
| `adminPassword` | string | 管理后台密码，默认 `admin` |
| `logDetail` | string | 日志详细级别：`off`/`basic`/`error`/`all` |
| `logBody` | boolean | 是否记录请求/响应体（需 `logDetail=all`） |
| `maxLogSizeMB` | number | 系统日志库大小上限（MB），超出自动轮转 |
| `maxRequestLogs` | number | 请求用量日志保留条数 |
| `activeLogFile` | number | 当前日志桶（1/2），轮转自动切换 |
| `providers` | array | 上游提供商列表 |
| `keys` | array | 虚拟密钥列表 |

### Provider 字段

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | string | 唯一标识 |
| `name` | string | 显示名称 |
| `baseUrl` | string | 上游基础地址，自动补全 `/v1` |
| `apiKey` | string | 上游 API Key |
| `enabled` | boolean | 是否启用 |
| `models` | string[] | 支持模型列表，请求模型不匹配时自动用 `defaultModel` 回退 |
| `defaultModel` | string | 回退模型，未配置则用 `models[0]` |
| `concurrency` | number | 并发限制，0 = 不限 |
| `timeout` | number | 上游请求超时毫秒数，0/省略 = 不超时 |
| `openaiEndpoint` | string | 可选，上游转发路径（如 `/chat/completions`），留空则自动基于 `baseUrl` 推导 |

### Virtual Key 字段

| 字段 | 类型 | 说明 |
|------|------|------|
| `key` | string | 客户端密钥（`sk-proxy-*`） |
| `name` | string | 显示名称 |
| `providerIds` | string[] | 授权 provider id 数组；`["all"]` 或 `["*"]` 表示全部启用集 |

**绑定规则**：key 的 `providerIds` 有具体值时（绑定 key），**不检查 provider 的 enabled**，按数组顺序作为降级优先级；`["all"]`/未绑定时走全局 enabled provider 集合。

### 多级代理

可在上游 provider 的 `baseUrl` 指向另一个 LLM Proxy 实例，形成多级链路。超时和连接断开信号会自动向两端传播：

```
客户端 → Level 1 → Level 2 → 上游 API
```

- Level 2 超时 → 返回 504 → Level 1 正常转发 → 客户端收到 504
- 客户端断开 → Level 1 abort → Level 2 abort → 上游连接断开
- 上游断开 → Level 2 destroy → Level 1 reader 抛错 → Level 1 destroy → 客户端感知

## 项目结构

```
├── cmd/llmproxy/main.go        # 入口（Go）
├── internal/
│   ├── proxy/                  # 代理引擎（转发、降级、流式、usage 解析）
│   ├── logstore/               # SQLite 日志存储（system_logs / request_logs）
│   ├── logging/                # 日志记录（内存环形缓冲 + 落库 + 轮转）
│   ├── config/                 # 配置加载与持久化
│   ├── handlers/               # 管理 API
│   ├── circuit/                # 熔断器
│   ├── auth/                   # 管理认证与 key 提取
│   └── server/                 # HTTP 服务与路由（内嵌前端）
├── src/                        # React 前端
├── config/config.json          # 运行配置（不在 git 中）
├── config.json.example         # 配置示例
├── logs/                       # SQLite 日志数据
├── Dockerfile
├── compose.yaml
└── dist/llmproxy               # 编译产物（Go 二进制，内嵌前端）
```

## 技术栈

后端: Go（标准库 + modernc.org/sqlite 纯 Go SQLite）
前端: React + Vite + Tailwind CSS + Lucide
部署: 单文件二进制 / Docker multi-arch（amd64 + arm64）

## 测试与构建

```bash
# 单元测试
go test ./...

# lint
npm run lint

# 构建
npm run build:go           # 前端 + 当前架构二进制
npm run build:go:arm64     # arm64 二进制
npm run build:go:all       # 前端 + amd64 + arm64
```
