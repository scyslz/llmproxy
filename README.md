# LLM Proxy

轻量级 LLM API 代理服务，提供 OpenAI 兼容接口和 Web 管理面板。支持多级代理串联，自动传播超时和连接断开信号。

## 核心功能

- **OpenAI 兼容代理** — 透明转发 `/v1/*` 到后端 LLM（chat/completions、embeddings 等）
- **流式传输** — 完整支持 SSE 流式响应，逐级转发
- **多提供商管理** — 同时配置多个提供商，动态切换激活
- **虚拟密钥** — `sk-proxy-*` 密钥体系，细粒度控制客户端权限
- **可配置上游超时** — 每个 provider 独立设置请求超时，超时自动向下游返回 504 并断开链路
- **链路断开传播** — 链路任意节点断开，自动向两端传播断开信号，防止连接挂死
- **Web 管理面板** — React SPA，管理提供商/密钥、实时日志、API Playground
- **双文件轮转日志** — 按大小自动轮转，Web UI 实时查看
- **请求用量日志 (SQLite)** — 记录每次请求的输入/输出/缓存 token、key、模型、耗时，支持按 key 与时间范围过滤
- **可选管理认证** — 管理后台密码保护
- **单文件二进制** — Node.js SEA 技术打包，免 Node 环境部署

## 快速开始

```bash
git clone <repo> && cd llmproxy
cp config.json.example config/config.json
# 编辑 config/config.json
npm run dev
```

访问 `http://localhost:4000` 打开管理面板。

## 部署

```bash
# 生产构建
npm run build && npm start

# 单文件二进制 (linux/amd64)
npm run build:sea && ./dist/llmproxy

# Docker
docker build -t llmproxy .
docker run -d -p 4000:4000 \
  -v $PWD/config/config.json:/app/config/config.json \
  llmproxy

# Docker Compose
docker compose up -d

# 停止
docker compose down
```

Docker Compose 直接使用镜像 `scyslz/llmproxy`，无需本地构建。容器内的配置目录为 `/app/config`（挂载宿主机的 `config/`），日志与 SQLite 请求用量数据位于 `/app/logs`，`compose.yaml` 已自动将这两个目录挂载到宿主机，配置修改后重启生效：`docker compose restart`。

## 配置

```jsonc
{
  "listen": ":4000",
  "enableVirtualKey": false,
  "enableAdminAuth": false,
  "adminPassword": "admin",
  "debug": false,
  "maxLogSizeMB": 20,
  "maxRequestLogs": 10000,    // 请求用量日志保留条数（SQLite，超出自动清理最旧记录）
  "providers": [
    {
      "id": "my-provider",
      "name": "My Provider",
      "baseUrl": "https://api.example.com/v1",
      "apiKey": "sk-xxx",
      "enabled": true,
      "models": ["gpt-4o"],
      "concurrency": 0,       // 0 = 不限并发
      "timeout": 120000       // 上游请求超时(ms)，0 或省略 = 不超时
    }
  ],
  "keys": [
    {
      "key": "sk-proxy-xxx",
      "name": "all",
      "providerIds": ["all"]
    }
  ]
}
```

### Provider 字段

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | string | 唯一标识 |
| `name` | string | 显示名称 |
| `baseUrl` | string | 上游基础地址，自动补全 `/v1` |
| `apiKey` | string | 上游 API Key |
| `enabled` | boolean | 是否启用（同时只能启用一个） |
| `models` | string[] | 支持模型列表，请求模型不匹配时自动用第一个回退 |
| `concurrency` | number | 并发限制，0 = 不限 |
| `timeout` | number | 上游请求超时毫秒数，0/省略 = 不超时 |

### 多级代理

可在上游 provider 的 `baseUrl` 指向另一个 LLM Proxy 实例，形成多级链路。
超时和连接断开信号会自动向两端传播：

```
客户端 → Level 1 → Level 2 → 上游 API
```

- Level 2 超时 → 返回 504 → Level 1 正常转发 → 客户端收到 504
- 客户端断开 → Level 1 abort → Level 2 abort → 上游连接断开
- 上游断开 → Level 2 destroy → Level 1 reader 抛错 → Level 1 destroy → 客户端感知

## 项目结构

```
├── server.ts                 # 服务端 (代理 + 管理 API，单文件)
├── src/                      # React 前端
│   ├── App.tsx
│   └── components/           # Header, ProviderCard, KeyManager, Playground, ...
├── config/
│   └── config.json           # 运行配置（不在 git 中）
├── config.json.example       # 配置示例
├── logs/                     # 轮转日志与 SQLite 请求用量数据
├── Dockerfile
├── compose.yaml              # Docker Compose 编排
└── vite.config.ts
```

## 技术栈

后端: TypeScript + Express + esbuild
前端: React + Vite + Tailwind CSS + Lucide
部署: Docker (Node 22 Alpine) / SEA 单文件二进制
