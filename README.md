# gpt-image-mcp

ä¸€ä¸ªè½»é‡çš„æœ¬åœ° **stdio MCP Server**ï¼Œè®© WorkBuddy / CodeBuddy ä½¿ç”¨ä½ è‡ªå·±çš„ OpenAI API Key æˆ– OpenAI-compatible API è°ƒç”¨ GPT Image ç”Ÿå›¾ã€‚

é‡ç‚¹ï¼š

- Go å•äºŒè¿›åˆ¶è¿è¡Œ
- å®˜æ–¹ MCP Go SDK
- `generate_image`ï¼šæ–‡ç”Ÿå›¾
- `edit_image`ï¼šå‚è€ƒå›¾ç”Ÿå›¾ / å›¾ç‰‡ç¼–è¾‘
- æ”¯æŒ 1â€“16 å¼ æœ¬åœ°å‚è€ƒå›¾
- æ”¯æŒå¯é€‰ PNG mask
- æ”¯æŒè‡ªå®šä¹‰ API Keyã€Base URLã€æ¨¡åž‹ã€è¾“å‡ºç›®å½•
- å‚è€ƒå›¾ç›´æŽ¥è¯»å– WorkBuddy å¯è®¿é—®çš„æœ¬åœ°è·¯å¾„ï¼Œä¸éœ€è¦åœ¨çº¿ä¸Šä¼ æœåŠ¡
- ç”Ÿæˆå›¾ç‰‡ä¿å­˜åˆ°æœ¬åœ°ï¼Œå¹¶åœ¨ä½“ç§¯å…è®¸æ—¶ä½œä¸º MCP image content è¿”å›žé¢„è§ˆ

## ä¸ºä»€ä¹ˆç”¨æœ¬åœ° MCP

WorkBuddy æ”¯æŒ stdio MCPï¼Œæœ¬åœ°æœåŠ¡é€šè¿‡ stdin/stdout ä¸Ž WorkBuddy é€šä¿¡ã€‚å›¾ç‰‡ API è¯·æ±‚ç”±æœ¬æœº MCP å‘å‡ºï¼Œå› æ­¤ API Key ä¸éœ€è¦äº¤ç»™é¢å¤–çš„åœ¨çº¿ MCP æœåŠ¡ã€‚

å‚è€ƒå›¾æµç¨‹ï¼š

```text
WorkBuddy é™„ä»¶ / æœ¬åœ°å›¾ç‰‡
        â†“ local path
edit_image(reference_images=[...])
        â†“ multipart/form-data
POST {OPENAI_BASE_URL}/images/edits
        â†“
ç”Ÿæˆå›¾ç‰‡ä¿å­˜åˆ°æœ¬åœ° + MCP é¢„è§ˆ
```

## è¦æ±‚

- Go 1.25+ï¼ˆè‡ªè¡Œç¼–è¯‘æ—¶ï¼‰
- ä¸€ä¸ªå¯ç”¨çš„ OpenAI API Keyï¼Œæˆ–å…¼å®¹ OpenAI Images API çš„ç¬¬ä¸‰æ–¹æœåŠ¡

## æž„å»º

```bash
git clone https://github.com/steamwo/gpt-image-mcp.git
cd gpt-image-mcp
go build -trimpath -o gpt-image-mcp .
```

Windowsï¼š

```powershell
go build -trimpath -o gpt-image-mcp.exe .
```

## WorkBuddy é…ç½®

WorkBuddy / CodeBuddy çš„ MCP é…ç½®æ”¯æŒ `stdio`ï¼š

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

ä¹Ÿå¯ä»¥ç”¨ WorkBuddy CLI æ·»åŠ æœ¬åœ° MCPï¼š

```bash
codebuddy mcp add --scope user gpt-image -- /absolute/path/to/gpt-image-mcp
```

API Key ç­‰çŽ¯å¢ƒå˜é‡ä»å»ºè®®å†™å…¥ MCP é…ç½®çš„ `env`ï¼Œä¸è¦æäº¤åˆ° Gitã€‚

## å·¥å…·

### `generate_image`

ä»Žæ–‡æœ¬ç”Ÿæˆå›¾ç‰‡ã€‚

å¸¸ç”¨å‚æ•°ï¼š

```json
{
  "prompt": "A cinematic photo of a rainy Tokyo alley at night",
  "size": "1536x1024",
  "quality": "high",
  "n": 1
}
```

### `edit_image`

ä½¿ç”¨å‚è€ƒå›¾è¿›è¡Œç”Ÿæˆæˆ–ç¼–è¾‘ã€‚`reference_images` æŽ¥å—ç»å¯¹è·¯å¾„æˆ– `file://` URIï¼š

```json
{
  "prompt": "Keep the subject unchanged, replace the background with a rainy Tokyo street at night",
  "reference_images": [
    "/absolute/path/reference.png"
  ],
  "quality": "high"
}
```

å¤šå‚è€ƒå›¾ï¼š

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

æœåŠ¡å™¨ä¼šä½¿ç”¨ OpenAI Images Edit API çš„ `image[]` multipart å­—æ®µå‘é€å¤šå¼ å‚è€ƒå›¾ã€‚

### Mask

å¯é€‰ `mask` å¿…é¡»æ˜¯ PNGï¼Œé€æ˜ŽåŒºåŸŸè¡¨ç¤ºå…è®¸ç¼–è¾‘çš„åŒºåŸŸï¼›mask ä¼šä½œç”¨äºŽç¬¬ä¸€å¼ å‚è€ƒå›¾ï¼š

```json
{
  "prompt": "Replace only the transparent masked area with a red sofa",
  "reference_images": ["/path/room.png"],
  "mask": "/path/mask.png"
}
```

## çŽ¯å¢ƒå˜é‡

| å˜é‡ | é»˜è®¤å€¼ | è¯´æ˜Ž |
|---|---|---|
| `OPENAI_API_KEY` | æ—  | å¿…å¡«ï¼›åœ¨å·¥å…·è°ƒç”¨æ—¶æ£€æŸ¥ |
| `OPENAI_BASE_URL` | `https://api.openai.com/v1` | API æ ¹åœ°å€ï¼Œåº”åŒ…å« `/v1` |
| `GPT_IMAGE_MODEL` | `gpt-image-2.6-sunburst` | é»˜è®¤å›¾ç‰‡æ¨¡åž‹ |
| `GPT_IMAGE_OUTPUT_DIR` | `./generated-images` | ç”Ÿæˆå›¾ç‰‡ç›®å½• |
| `GPT_IMAGE_TIMEOUT` | `10m` | å•æ¬¡ API è¯·æ±‚è¶…æ—¶ |
| `GPT_IMAGE_EMBED_MAX_BYTES` | `8388608` | å•æ¬¡ MCP ç»“æžœå…è®¸å†…åµŒçš„å›¾ç‰‡æ€»å­—èŠ‚æ•°ï¼›`0` è¡¨ç¤ºåªè¿”å›žæ–‡ä»¶è·¯å¾„ |
| `GPT_IMAGE_ALLOWED_DIRS` | ç©º | å¯é€‰å‚è€ƒå›¾ç›®å½•ç™½åÊB7–6W¾òož¦ë¢†£ž’ë–¢ºã¢¾ï–>[’îïš?šr³–rÃ–nûž&¢Þ¿–úð)ð=A9%}=I9%iQ%=9€ðƒž¦èðƒ–>¿¦$=Á•¹$µ=É…¹¥é…Ñ¥½¹€¡•…‘•Èð)ð=A9%}AI=)Q€ðƒž¦èðƒ–>¿¦$=Á•¹$µAÉ½©•Ñ€¡•…‘•Èð(+ž²³’â'šZç’â·¢ö³ž’ë’ú/¾òh()©Í½¹Œ(‰•¹Øˆèì(€€‰=A9%}A%}-dˆè€‰å½ÕÈµ­•äˆ°(€€‰=A9%}	M}UI0ˆè€‰¡ÑÑÁÌè¼½å½ÕÈµ…Ñ•Ý…ä¹•á…µÁ±”½ØÄˆ°(€€‰AQ}%5}5=0ˆè€‰å½ÕÈµÍÕÁÁ½ÉÑ•µ¥µ…”µµ½‘•°ˆ)ô)€((øƒ–ó–ºçšŸ–>[–Ï’ê;’â·¢ö³šr7–*‡šb¿–B›žrš¶–º{ž:À€½¥µ…•Ì½•¹•É…Ñ¥½¹Í€ƒ–J0€½¥µ…•Ì½•‘¥ÑÍƒ¾ò3ž&ç–"¯šb¼µÕ±Ñ¥Á…ÉÐƒ–’k–>¢–nûžj¥µ…•mu€ƒ–¶_šº×Ž((ŒŒƒšr³–rÃšZ’îÛ–º'– ()•‘¥Ñ}¥µ…•€ƒ’òkš*+š2–ºkžjšr³–rÃ–>¢–nû–>G¦–"Ã’öƒ¦7žö»žj–nûž&A'Ž¦îc¢º“š–×’â/¾ò3’âë’ê–ƒ–ºä]½É­	Õ‘‘äƒžj¦f’îÛ’âÓš^Ûžn»–öW¾ò3–>¿’î—¢¾ï–>[’îïš?šr³–rÀA9½)A½]•‰@ƒ¢Þ¿–úŽ(+–ššzs’öƒ–â3šro¦fC–"Û–>¿¢¾ï–>[žn»–öW¾ò3¢¾ß¢ºûžö»¾òh()‰…Í )AQ}%5}11=]}%ILô½…±±½Ý•½ÕÁ±½…‘Ìè½…¹½Ñ¡•È½…±±½Ý•½Á…Ñ )€()]¥¹‘½ÝÌƒ’öÿžR €í€ƒ–"¦jS–’k’â«žn»–öWŽ((ŒŒƒ–öO–&4AP%µ…”ƒš¢‡–z,(+š¢‡–z/–>c–2[¢ú–þ¯¾ò3š&’î—š¢‡–z/–B7šÊ‡šr'–gš¶ï–r£–Þ—–ß¦ï¢úG’â·Ž¦îc¢º“–ó–>«šb¿’â’â«ž:¿–Š–>c¦?¦îc¢º“–ó¾ò3¦j?š^Û–>¿’î—¦k¢þAQ}%5}5=1€ƒš"[š¾?š²‡–Þ—–ß¢ÂžR£žjµ½‘•±€ƒ–>šVÃ¢šžn[Ž((ŒŒƒ–ò–>D()‰…Í )¼Ñ•ÍÐ€¸¼¸¸¸)¼‰Õ¥±€¸¼¸¸¸)€