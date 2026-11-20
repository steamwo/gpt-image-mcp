# gpt-image-mcp

一个面向 Agent 的本地 **stdio MCP 图像能力适配器**。目标不是让 Agent 学会 OpenAI Images API，而是让本身不支持自定义生图模型调用的 Agent 只需要理解两个动作：

- `generate_image`：从文本生成图片
- `edit_image`：基于参考图生成或编辑图片

模型差异、OpenAI-compatible 网关差异、multipart、base64、附件路径、MIME、安全限制和结果落盘由 MCP 内部处理。

## 特性

- Go 单二进制，使用官方 MCP Go SDK
- Agent-facing schema 保持简单：通常只需要 `prompt`
- `edit_image` 支持 1–16 张参考图
- 参考图支持本地路径、`file://`、公共 HTTP(S) URL、base64 data URI
- 支持可选 PNG mask
- 自动 provider profile：OpenAI 官方端点使用完整能力，第三方端点默认使用兼容参数子集
- API Key 可选，支持自定义 HTTP headers
- 成功结果同时返回 MCP image content 和 typed structured output
- 错误返回结构化 `code / retryable / parameter / status / hint`
- 本地路径白名单会解析 symlink 后再检查
- 远程参考图默认阻止 loopback、私网、link-local 地址，降低 SSRF 风险
- 输出图片根据真实文件内容检测 MIME，不盲信扩展名或 provider 声明

## 构建

```bash
git clone https://github.com/steamwo/gpt-image-mcp.git
cd gpt-image-mcp
go build -trimpath -o gpt-image-mcp .
```

Windows：

```powershell
go build -trimpath -o gpt-image-mcp.exe .
```

## MCP 配置示例

```jsonc
{
  "mcpServers": {
    "gpt-image": {
      "type": "stdio",
      "command": "/absolute/path/to/gpt-image-mcp",
      "env": {
        "OPENAI_API_KEY": "sk-...",
        "OPENAI_BASE_URL": "https://api.openai.com/v1",
        "GPT_IMAGE_MODEL": "gpt-image-2.6-sunburst",
        "GPT_IMAGE_OUTPUT_DIR": "/absolute/path/to/generated-images"
      }
    }
  }
}
```

本地或无鉴权的 OpenAI-compatible 服务可以不设置 `OPENAI_API_KEY`：

```jsonc
"env": {
  "OPENAI_BASE_URL": "http://127.0.0.1:8000/v1",
  "GPT_IMAGE_MODEL": "my-image-model",
  "GPT_IMAGE_PROFILE": "compatible"
}
```

需要其他鉴权方式时：

```jsonc
"env": {
  "OPENAI_BASE_URL": "https://gateway.example/v1",
  "OPENAI_EXTRA_HEADERS_JSON": "{\"X-API-Key\":\"secret\"}",
  "GPT_IMAGE_MODEL": "image-model"
}
```

## 工具

### `generate_image`

最小调用：

```json
{
  "prompt": "A cinematic photo of a rainy Tokyo alley at night"
}
```

可选参数：`model`、`size`、`quality`、`n`、`transparent`。

### `edit_image`

```json
{
  "prompt": "Keep the subject unchanged and replace the background with a rainy Tokyo street at night",
  "reference_images": [
    "/absolute/path/reference.png"
  ]
}
```

参考图也可以混用不同来源：

```json
{
  "prompt": "Create one product photo using all referenced items",
  "reference_images": [
    "file:///path/item-1.png",
    "https://example.com/item-2.jpg",
    "data:image/png;base64,iVBORw0KGgo..."
  ]
}
```

`mask` 接受相同的来源形式，但最终内容必须是 PNG。

## Provider profile

`GPT_IMAGE_PROFILE` 控制 MCP 向下游 API 发送哪些可选参数：

| Profile | 用途 |
|---|---|
| `auto` | 默认。api.openai.com 使用 `openai`，其他端点使用 `compatible` |
| `openai` | 使用完整 OpenAI Images 参数能力 |
| `compatible` | 面向常见 OpenAI-compatible 网关，只发送广泛支持的参数 |
| `minimal` | 最大兼容模式，尽量只发送核心字段 |

高级 provider-specific 参数不再暴露给 Agent，可通过环境变量设置，并且只会在当前 profile 支持时发送：

```bash
GPT_IMAGE_DEFAULT_BACKGROUND=auto
GPT_IMAGE_DEFAULT_OUTPUT_FORMAT=png
GPT_IMAGE_DEFAULT_COMPLEXITY=high
GPT_IMAGE_DEFAULT_INPUT_FIDELITY=high
GPT_IMAGE_DEFAULT_OUTPUT_COMPRESSION=90
```

## Structured output

成功结果包含：

```json
{
  "images": [
    {
      "path": "/.../gpt-image-123.png",
      "mime_type": "image/png",
      "size_bytes": 123456,
      "embedded_preview": true
    }
  ],
  "model": "gpt-image-2.6-sunburst",
  "count": 1,
  "provider_profile": "openai"
}
```

错误会通过 MCP `IsError` 返回，并附带机器可读字段，例如 `invalid_input`、`authentication_failed`、`provider_busy`、`network_error`、`timeout`。

## 安全设置

本地文件白名单：

```bash
GPT_IMAGE_ALLOWED_DIRS=/allowed/uploads:/another/allowed/path
```

Windows 使用 `;` 分隔。路径会解析 symlink 后再检查。

远程参考图默认允许公共 HTTP(S) URL，但阻止 loopback、RFC1918/private、link-local 等地址。只有明确需要访问本机/内网附件服务时才开启：

```bash
GPT_IMAGE_ALLOW_PRIVATE_REMOTE_IMAGES=true
```

也可以完全关闭远程参考图：

```bash
GPT_IMAGE_ALLOW_REMOTE_IMAGES=false
```

## 主要环境变量

| 变量 | 默认值 |
|---|---|
| `OPENAI_API_KEY` | 空，可选 |
| `OPENAI_BASE_URL` | `https://api.openai.com/v1` |
| `GPT_IMAGE_MODEL` | `gpt-image-2.6-sunburst` |
| `GPT_IMAGE_PROFILE` | `auto` |
| `GPT_IMAGE_OUTPUT_DIR` | `./generated-images` |
| `GPT_IMAGE_TIMEOUT` | `10m` |
| `GPT_IMAGE_EMBED_MAX_BYTES` | `8388608` |
| `GPT_IMAGE_ALLOW_REMOTE_IMAGES` | `true` |
| `GPT_IMAGE_ALLOW_PRIVATE_REMOTE_IMAGES` | `false` |

## 开发

```bash
go vet ./...
go test ./...
go build ./...
```
