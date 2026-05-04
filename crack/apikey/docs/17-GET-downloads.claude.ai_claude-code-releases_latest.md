# 17. GET https://downloads.claude.ai/claude-code-releases/latest

**阶段**：杂噪 **状态码**：200 **请求大小**：0 B **响应大小**：7 B

**用途**：自更新版本号又拉一次。

## 请求行

```
GET https://downloads.claude.ai/claude-code-releases/latest
```

## 请求头（共 5 个）

| Header | Value |
|---|---|
| accept | application/json, text/plain, */* |
| accept-encoding | gzip, br |
| user-agent | axios/1.13.6 |
| host | downloads.claude.ai |
| connection | close |

## 请求体

_无_

## 响应头（共 19 个）

| Header | Value |
|---|---|
| x-guploader-uploadid | AAVLpEjkpqcTzseWDIemUFoNdQKNYzkHPKkEOto-AcInwL83mSsnYZmcQYbDeeRgv_cdc2HnKkKVrg |
| x-goog-generation | 1777601117782171 |
| x-goog-metageneration | 1 |
| x-goog-stored-content-encoding | identity |
| x-goog-stored-content-length | 7 |
| x-goog-hash | crc32c=SuKVIw==, md5=0LdxGc8odZ5F81qbL9XiCQ== |
| x-goog-storage-class | STANDARD |
| accept-ranges | bytes |
| content-length | 7 |
| server | UploadServer |
| via | 1.1 google |
| date | Sun, 03 May 2026 15:54:27 GMT |
| last-modified | Fri, 01 May 2026 02:05:17 GMT |
| etag | "d0b77119cf28759e45f35a9b2fd5e209" |
| content-type | text/plain |
| age | 0 |
| cache-control | public,no-cache,max-age=0 |
| alt-svc | h3=":443"; ma=2592000,h3-29=":443"; ma=2592000 |
| connection | close |

## 响应体

- **Content-Type**：`text/plain`
- **解码后大小**：`7` B
- **格式**：非 JSON / 文本

### 内容
```
2.1.126
```

---
_原始 JSON_：[`rows/17-GET-downloads.claude.ai_claude-code-releases_latest.json`](../rows/17-GET-downloads.claude.ai_claude-code-releases_latest.json)
