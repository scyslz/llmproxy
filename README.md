# LLM Proxy

一个轻量级的 LLM API 代理服务，提供 OpenAI 兼容接口和 Web 管理面板。

## 核心功能

- **OpenAI 兼容代理** — 透明转发 `/v1/*` 请求（chat/completions、embeddings 等）到后端 LLM 提供商，支持流式传输和并发控制
- **多提供商管理** — 同时配置多个提供商（Gemini、DeepSeek、OpenAI 等），动态切换激活
- **虚拟密钥** — `sk-proxy-*` 密钥体系，细粒度控制客户端访问权限
- **Web 管理面板** — React SPA，管理提供商、密钥、查看日志、API Playground 在线测试
- **双文件轮转日志** — 按大小自动轮转，Web UI 实时查看
- **可选管理认证** — 管理后台密码保护
- **Single Executable Binary** — Node.js SEA 技术打包为单文件二进制，免 Node 环境部署

## 快速开始

```bash
git clone https://github.com/yourusername/llmproxy.git
cd llmproxy
cp config.json.example config.json
# 编辑 config.json 配置你的提供商和密钥
npm run dev
```

访问 `http://localhost:4000` 打开管理面板。

## 构建部署

```bash
# 生产构建
npm run build
npm start

# 单文件二进制 (linux/amd64)
npm run build:sea
./dist/llmproxy

# Docker
docker build -t llmproxy .
docker run -d -p 4000:4000 -v $PWD/config.json:/app/config.json llmproxy
```

## 项目结构

```
├── server.ts                 # Express 服务端 (代理 + 管理 API)
├── src/                      # React 前端
│   ├── App.tsx
│   └── components/           # Header, ProviderCard, KeyManager, Playground, TerminalLogs ...
├── vite.config.ts
├── Dockerfile
├── config.json
└── logs/                     # 日志
```

## 配置

| 字段 | 说明 |
|------|------|
| `providers` | LLM 提供商列表（baseUrl、apiKey、models、concurrency） |
| `keys` | 虚拟密钥列表（`sk-proxy-*`，可限定 providerIds） |
| `enableVirtualKey` | 是否开启虚拟密钥认证 |
| `enableAdminAuth` | 是否开启管理面板登录 |
| `listen` | 监听地址，默认 `:3000` |

## 技术栈

- **后端**: TypeScript + Express + esbuild
- **前端**: React + Vite + Tailwind CSS + Lucide
- **部署**: Docker (Node 22 Alpine) / SEA 单文件二进制 / 
