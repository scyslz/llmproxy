# LLM Proxy

一个轻量级的 LLM API 代理服务，支持多个 AI 提供商的统一管理和转发。

## 功能特性

- 🔄 **多提供商支持**：同时配置和管理多个 LLM 提供商（OpenAI、DeepSeek、CloudBase 等）
- 🔑 **虚拟密钥管理**：通过虚拟 API Key 统一访问不同提供商
- 🎛️ **Web 管理界面**：直观的 Web UI 管理提供商和配置
- ⚡ **动态路由**：自动根据请求路由到对应的提供商
- 📊 **健康检查**：内置健康检查端点
- 🔧 **热重载配置**：支持运行时管理提供商状态

## 快速开始

### 安装

```bash
# 克隆仓库
git clone https://github.com/yourusername/llmproxy.git
cd llmproxy

# 编译
go build -o llmproxy main.go

# 或者直接运行
go run main.go
```

### 配置

编辑 `config.json` 配置文件：

```json
{
  "listen": ":4000",
  "debug": false,
  "providers": [
    {
      "id": "cloudbase",
      "name": "cloudbase1",
      "base_url": "https://api.example.com/v1",
      "api_key": "your-api-key",
      "models": ["model-name"],
      "enabled": true
    }
  ],
  "virtual_keys": [
    {
      "id": "sk-test-key",
      "provider_ids": ["cloudbase"],
      "rate_limit": 0,
      "enabled": true
    }
  ]
}
```

### 运行

```bash
# 启动服务
./llmproxy

# 或者使用 systemd (Linux)
sudo cp llmproxy.service /etc/systemd/system/
sudo systemctl enable llmproxy
sudo systemctl start llmproxy
```

服务将在 `http://localhost:4000` 启动。

## API 使用

### 基本用法

LLM Proxy 兼容 OpenAI API 格式，可以直接使用 OpenAI 客户端：

```python
import openai

client = openai.OpenAI(
    api_key="sk-test-key",  # 使用虚拟密钥或提供商 API Key
    base_url="http://localhost:4000/v1"
)

response = client.chat.completions.create(
    model="model-name",
    messages=[{"role": "user", "content": "Hello!"}]
)
print(response.choices[0].message.content)
```

### 主要端点

- `POST /v1/chat/completions` - 聊天补全
- `POST /v1/completions` - 文本补全
- `GET /v1/models` - 列出可用模型
- `GET /health` - 健康检查

### 管理 API

- `GET /api/providers` - 列出所有提供商
- `POST /api/providers` - 添加/更新提供商
- `DELETE /api/providers/{id}` - 删除提供商

## Web 管理界面

访问 `http://localhost:4000` 打开 Web 管理界面，可以：

- 查看所有配置的提供商
- 启用/禁用提供商
- 添加/编辑/删除提供商
- 查看提供商状态和配置

## 配置说明

### Provider 配置

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | string | 提供商唯一标识 |
| `name` | string | 提供商显示名称 |
| `base_url` | string | API 基础 URL |
| `api_key` | string | API 密钥 |
| `models` | []string | 支持的模型列表 |
| `enabled` | bool | 是否启用 |

### Virtual Key 配置

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | string | 虚拟密钥 ID |
| `provider_ids` | []string | 关联的提供商 ID 列表 |
| `rate_limit` | int | 速率限制（0 表示无限制） |
| `enabled` | bool | 是否启用 |

## 部署

### 二进制部署

```bash
# 编译 Linux 版本
GOOS=linux GOARCH=amd64 go build -o llmproxy main.go

# 编译 ARM64 版本
GOOS=linux GOARCH=arm64 go build -o llmproxy-arm64 main.go
```

### Docker 部署（可选）

```dockerfile
FROM golang:1.19-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o llmproxy main.go

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/llmproxy .
COPY config.json .
EXPOSE 4000
CMD ["./llmproxy"]
```

## 开发

### 项目结构

```
llmproxy/
├── main.go              # 主程序入口
├── config/              # 配置相关
│   └── config.go
├── handler/             # HTTP 处理器
│   ├── handler.go
│   └── adminhandler.go
├── key/                 # 密钥管理
│   └── key.go
├── provider/            # 提供商管理
│   └── provider.go
├── web/                 # Web 界面
│   └── index.html
├── config.json          # 配置文件
└── llmproxy.service     # systemd 服务文件
```

### 构建

```bash
# 开发模式
go run main.go

# 生产构建
go build -ldflags "-s -w" -o llmproxy main.go
```

## 许可证

MIT License

## 贡献

欢迎提交 Issue 和 Pull Request！

## 作者

Your Name - [@yourusername](https://github.com/yourusername)

## 更新日志

### v1.0.0 (2024-01-01)

- ✨ 初始版本发布
- ✨ 支持多提供商管理
- ✨ 添加 Web 管理界面
- ✨ 实现虚拟密钥功能
