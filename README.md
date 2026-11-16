# gpt-image-mcp

一个轻量的本地 **stdio MCP Server**，让 WorkBuddy / CodeBuddy 使用你自己的 OpenAI API Key 或 OpenAI-compatible API 调用 GPT Image 生图。

重点：

- Go 单二进制运行
- 官方 MCP Go SDK
- `generate_image`：文生图
- `edit_image`：参考图生图 / 图片编辑
- 支持 1–16 张本地参考图
- 支持可选 PNG mask
- 支持自定义 API Key、Base URL、模型、输出目录
- 参考图直接读取 WorkBuddy 可访问的本地路径，不需要在线上传服务
- 生成图片保存到本地，并在体积允许时作为 MCP image content 返回预览

## 为什么用本地 MCP

WorkBuddy 支持 stdio MCP，本地服务通过 stdin/stdout 与 WorkBuddy 通信。图片 API 请求由本机 MCP 发出，因此 API Key 不需要交给额外的在线 MCP 服务。

参考图流程：

```text
WorkBuddy 附件 / 本地图片
        ↓ local path
edit_image(reference_images=[...])
        ↓ multipart/form-data
POST {OPENAI_BASE_URL}/images/edits
        ↓
生成图片保存到本地 + MCP 预览
```

## 要求

- Go 1.25+（自行编译时）
- 一个可用的 OpenAI API Key，或兼容 OpenAI Images API 的第三方服务

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

## WorkBuddy 配置

WorkBuddy / CodeBuddy 的 MCP 配置支持 `stdio`：

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
      },
      "description": "Generate and edit images with GPT Image"
    }
  }
}
```

也可以用 WorkBuddy CLI 添加本地 MCP：

```bash
codebuddy mcp add --scope user gpt-image -- /absolute/path/to/gpt-image-mcp
```

API Key 等环境变量仍建议写入 MCP 配置的 `env`，不要提交到 Git。

## 工具

### `generate_image`

从文本生成图片。

常用参数：

```json
{
  "prompt": "A cinematic photo of a rainy Tokyo alley at night",
  "size": "1536x1024",
  "quality": "high",
  "n": 1
}
```

### `edit_image`

使用参考图进行生成或编辑。`reference_images` 接受绝对路径或 `file://` URI：

```json
{
  "prompt": "Keep the subject unchanged, replace the background with a rainy Tokyo street at night",
  "reference_images": [
    "/absolute/path/reference.png"
  ],
  "quality": "high"
}
```

多参考图：

```json
{
  "prompt": "Create one product photo using all items from the reference images",
  "reference_images": [
    "/path/item-1.png",
    "/path/item-2.jpg",
    "/path/item-3.webp"
  ]
}
```

服务器会使用 OpenAI Images Edit API 的 `image[]` multipart 字段发送多张参考图。

### Mask

可选 `mask` 必须是 PNG，透明区域表示允许编辑的区域；mask 会作用于第一张参考图：

```json
{
  "prompt": "Replace only the transparent masked area with a red sofa",
  "reference_images": ["/path/room.png"],
  "mask": "/path/mask.png"
}
```

## 环境变量

| 变量 | 默认值 | 说明 |
|---|---|---|
| `OPENAI_API_KEY` | 无 | 必填；在工具调用时检查 |
| `OPENAI_BASE_URL` | `https://api.openai.com/v1` | API 根地址，应包含 `/v1` |
| `GPT_IMAGE_MODEL` | `gpt-image-2.6-sunburst` | 默认图片模型 |
| `GPT_IMAGE_OUTPUT_DIR` | `./generated-images` | 生成图片目录 |
| `GPT_IMAGE_TIMEOUT` | `10m` | 单次 API 请求超时 |
| `GPT_IMAGE_EMBED_MAX_BYTES` | `8388608` | 单次 MCP 结果允许内嵌的图片总字节数；`0` 表示只返回文件路径 |
| `GPT_IMAGE_ALLOWED_DIRS` | 空 | 可选参考图目录白名单；空表示允许读取任意本地图片路径 |
| `OPENAI_ORGANIZATION` | 空 | 可选 `OpenAI-Organization` header |
| `OPENAI_PROJECT` | 空 | 可选 `OpenAI-Project` header |

第三方中转示例：

```jsonc
"env": {
  "OPENAI_API_KEY": "your-key",
  "OPENAI_BASE_URL": "https://your-gateway.example/v1",
  "GPT_IMAGE_MODEL": "your-supported-image-model"
}
```

> 兼容性取决于中转服务是否真正实现 `/images/generations` 和 `/images/edits`，特别是 multipart 多参考图的 `image[]` 字段。

## 本地文件安全

`edit_image` 会把指定的本地参考图发送到你配置的图片 API。默认情况下，为了兼容 WorkBuddy 的附件临时目录，可以读取任意本地 PNG/JPEG/WebP 路径。

如果你希望限制可读取目录，请设置：

```bash
GPT_IMAGE_ALLOWED_DIRS=/allowed/uploads:/another/allowed/path
```

Windows 使用 `;` 分隔多个目录。

## 当前 GPT Image 模型

模型变化较快，所以模型名没有写死在工具逻辑中。默认值只是一个环境变量默认值，随时可以通过 `GPT_IMAGE_MODEL` 或每次工具调用的 `model` 参数覆盖。

## 开发

```bash
go test ./...
go build ./...
```
