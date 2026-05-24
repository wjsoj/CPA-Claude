# Kiro-CLI 抓包档案

`kiro-cli` (= Amazon Q for CLI 2.4.1, Rust + aws-sdk-rust/1.3.16, Linux) 在 Linux 下一次完整会话的网络流量，**与 `crack/oauth/` `crack/apikey/` `crack/login/`（Anthropic Claude Code 抓包）严格分开** —— 二者协议、鉴权、上游服务都不同。

- Anthropic Claude Code：HTTPS + JSON + OAuth Bearer / API Key → `api.anthropic.com`
- **Kiro-CLI**：HTTPS + AWS Smithy 协议（x-amz-json-1.0 / 1.1）+ event-stream + 双轨认证 → `q.us-east-1.amazonaws.com` + AWS Cognito + Kiro 自建 token 服务

## 会话概览

| | 值 |
|---|---|
| 抓包工具 | Whistle 2.x（本地 mitm） |
| 客户端 | Kiro-CLI 2.4.1（仓库即 [`hank9999/kiro.rs`](https://github.com/hank9999/kiro.rs)，上游为 AWS amazon-q-developer-cli） |
| 操作系统 | Linux x86_64，Rust 1.92.0 |
| AWS SDK | `aws-sdk-rust/1.3.16` |
| 抓包时长 | 约 162 秒（48 条请求） |
| 业务回合数 | 11 次 `GenerateAssistantResponse` |
| 模型 | `CLAUDE_SONNET_4_5_V1_0`（由 Kiro 服务端按 modelId 字符串映射） |

## 目录结构

```
crack/kiro/
├── README.md          ← 本文件
├── rows/              ← 48 个 JSON 原文（已解压 / base64 解码，bodies 内联）—— "已登录会话的业务流"
│   ├── 01-POST-prod.us-east-1.auth.desktop.kiro.dev_refreshToken.json
│   ├── ...
│   └── _manifest.json
├── docs/              ← 48 个独立 markdown
│   ├── 01-POST-prod.us-east-1.auth.desktop.kiro.dev_refreshToken.md
│   └── ...
└── login/             ← Login / Logout PKCE 链路（独立子档案）—— "凭据生命周期"
    ├── README.md      ← PKCE 流程总览 + 三端点对比 + 与 Anthropic login 对比 ★
    ├── raw/kiro-login-session-full.json
    ├── rows/          ← 14 个 JSON
    └── docs/          ← 14 个 markdown
```

原始 dump：[`crack/raw/kiro-session-full.json`](../raw/kiro-session-full.json)（直接来自 Whistle `/cgi-bin/get-data` 的全量结构，含 `req.base64 / res.base64`，未脱敏前请勿外发）。

## 4 个上游 host 一览

| Host | 协议 | 鉴权 | 用途 | 本会话条数 |
|---|---|---|---|---|
| `prod.us-east-1.auth.desktop.kiro.dev` | HTTPS + JSON | 无（body 自证） | **Kiro 自有 token 服务**：refresh accessToken/refreshToken | 1 |
| `cognito-identity.us-east-1.amazonaws.com` | HTTPS + Smithy `x-amz-json-1.1` | 无 | AWS Cognito Identity Pool：`GetCredentialsForIdentity` 换 STS 临时凭据 | 1 |
| `q.us-east-1.amazonaws.com` | HTTPS + Smithy `x-amz-json-1.0` + event-stream | **Bearer = Kiro accessToken** | CodeWhisperer Runtime + Streaming：`ListAvailableModels` / `GenerateAssistantResponse` / `SendTelemetryEvent` | 23 |
| `client-telemetry.us-east-1.amazonaws.com` | HTTPS + 纯 JSON | **SigV4 with STS 临时凭据** | Amazon Toolkit Telemetry 通用打点 | 23 |

## 关键发现

1. **双轨鉴权并存**
   - **Bearer 路径**：业务接口（`q.us-east-1`）用 Kiro 自家的 `accessToken` 当 Bearer，**完全不经过 AWS SigV4**。
   - **SigV4 路径**：toolkit telemetry（`client-telemetry`）用 #02 拿到的 STS 临时凭据签 SigV4（service=`execute-api`）。
   - 两套凭据**互不混用**：Bearer 不会出现在 telemetry 请求里，SigV4 也不会出现在业务请求里。

2. **Kiro accessToken 不是标准 OAuth、不是 AWS 凭据**
   - 形如 `aoaAAAAAGo...:{ECDSA-sig}`，约 220 char，带服务端签名校验。
   - refresh 时 body 只有 `{refreshToken}`，**没有 `client_id`/`grant_type`**。
   - refresh 端点是 Kiro 自建的 `prod.us-east-1.auth.desktop.kiro.dev/refreshToken`，跟 AWS Cognito Hosted UI / Identity Pool 都不一样。

3. **`profileArn` 是公共共享 arn**
   `arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK`
   该 AWS account `699475941385` 是 Amazon 自家给 Q for Free Tier 用户共享的 profile —— **不是用户自己的 AWS account**。所有 Kiro 用户的请求里 profileArn 都是这一串。

4. **AWS event-stream 二进制响应**
   `GenerateAssistantResponse` 响应是 `Content-Type: application/vnd.amazon.eventstream` 二进制帧（12-byte prelude + headers + payload + CRC），不是 SSE。常见 event-type：`initial-response` / `assistantResponseEvent` / `toolUseEvent` / `codeReferenceEvent` / `messageMetadataEvent`（末帧含 token 计费）。

5. **没有 system prompt 字段**
   Kiro 把所有"系统指令"和"上下文注入"都拼到 `userInputMessage.content` 里（用 `--- CONTEXT ENTRY BEGIN ---` 分隔块），**不像 Anthropic 协议有独立的 `system` 顶层字段**。tools 列表也没有 —— 工具定义在 CodeWhisperer 服务端按 `modelId` 配置，客户端只在 `userInputMessageContext.toolResults` 里回上一轮工具产物。

6. **业务 telemetry vs Toolkit telemetry 是两套通道**
   - **业务 telemetry**（`SendTelemetryEvent` → `q.us-east-1`）：每个 turn 上报一次 `chatAddMessageEvent`，含 `timeToFirstChunkMilliseconds / timeBetweenChunks[] / cwsprChatPromptLength` 等"对话质量"度量。
   - **Toolkit telemetry**（`/metrics` → `client-telemetry`）：度量 CLI 子命令调用、心跳、UI 交互等"产品使用"度量。
   - 每个 chat turn 一般触发 1 条业务 telemetry + 2 条 toolkit telemetry。

7. **Telemetry 与对话流异步解耦**
   `SendTelemetryEvent` 不会阻塞下一轮对话 —— 抓包里 turn 7 的 telemetry（#33）甚至晚于 turn 8、turn 9。CLI 把事件丢到本地队列，后台 worker 按容量节流上报。

## 会话时序总览（请求级）

按发起时间排序，48 条全列：

| # | 阶段 | host (简) | 端点 / x-amz-target | 大小 (req → res) |
|---|---|---|---|---|
| 01 | 启动 — 身份 | auth.desktop.kiro.dev | `POST /refreshToken` | 249 → 596 |
| 02 | 启动 — 身份 | cognito-identity | `GetCredentialsForIdentity` | 63 → 1756 |
| 03 | 启动 — bootstrap | q.us-east-1 | `ListAvailableModels` | 102 → 5952 |
| 04 | 启动 — Telemetry | client-telemetry | `/metrics` (dailyHeartbeat) | 363 → 0 |
| 05 | 启动 — Telemetry | client-telemetry | `/metrics` (cliSubcommandExecuted) | 568 → 0 |
| 06 | **Turn 1** | q.us-east-1 | `GenerateAssistantResponse` | 49 KB → 6 KB |
| 07 | Turn 1 Telemetry | q.us-east-1 | `SendTelemetryEvent` | 1089 → 2 |
| 08-09 | Telemetry | client-telemetry | `/metrics` ×2 | 2.2K+1.6K → 0 |
| 10 | **Turn 2** | q.us-east-1 | `GenerateAssistantResponse` | 50 KB → 3 KB |
| 11 | Turn 2 Telemetry | q.us-east-1 | `SendTelemetryEvent` | 849 → 2 |
| 12-13 | Telemetry | client-telemetry | `/metrics` ×2 | — |
| 14 | **Turn 3** | q.us-east-1 | `GenerateAssistantResponse` | 51 KB → 3 KB |
| 15 | Turn 3 Telemetry | q.us-east-1 | `SendTelemetryEvent` | 798 → 2 |
| 16 | **Turn 4** | q.us-east-1 | `GenerateAssistantResponse` | 65 KB → 9 KB |
| 17-18 | Telemetry | client-telemetry | `/metrics` ×2 | — |
| 19 | Turn 4 Telemetry (late) | q.us-east-1 | `SendTelemetryEvent` | 1318 → 2 |
| 20-21 | Telemetry | client-telemetry | `/metrics` ×2 | — |
| 22 | **Turn 5** (小) | q.us-east-1 | `GenerateAssistantResponse` | 1.3 KB → 11 KB |
| 23 | **Turn 6** | q.us-east-1 | `GenerateAssistantResponse` | 67 KB → 9 KB |
| 24 | Turn 6 Telemetry | q.us-east-1 | `SendTelemetryEvent` | 1154 → 2 |
| 25 | **Turn 7** | q.us-east-1 | `GenerateAssistantResponse` | 119 KB → 11 KB |
| 26-28 | Telemetry | client-telemetry | `/metrics` ×3 | — |
| 29 | **Turn 8** | q.us-east-1 | `GenerateAssistantResponse` | 124 KB → 12 KB |
| 30 | Turn 8 Telemetry | q.us-east-1 | `SendTelemetryEvent` | 1320 → 2 |
| 31-32 | Telemetry | client-telemetry | `/metrics` ×2 | — |
| 33 | Turn 7 Telemetry (very late) | q.us-east-1 | `SendTelemetryEvent` | 1396 → 2 |
| 34 | **Turn 9** | q.us-east-1 | `GenerateAssistantResponse` | 130 KB → 16 KB |
| 35-36 | Telemetry | client-telemetry | `/metrics` ×2 | — |
| 37 | **Turn 10** (最长) | q.us-east-1 | `GenerateAssistantResponse` | 134 KB → 53 KB |
| 38 | Turn 10 Telemetry | q.us-east-1 | `SendTelemetryEvent` **aborted** | — |
| 39 | Turn 10 Telemetry retry | q.us-east-1 | `SendTelemetryEvent` | 1719 → 2 |
| 40-41 | Telemetry | client-telemetry | `/metrics` ×2 | — |
| 42 | Telemetry late | q.us-east-1 | `SendTelemetryEvent` | 1808 → 2 |
| 43-44 | Telemetry | client-telemetry | `/metrics` ×2 (4.4K+2K) | — |
| 45 | **Turn 11**（末轮） | q.us-east-1 | `GenerateAssistantResponse` | 129 KB → 3 KB |
| 46 | Turn 11 Telemetry | q.us-east-1 | `SendTelemetryEvent` | 782 → 2 |
| 47 | Telemetry | client-telemetry | `/metrics` | 1999 → 0 |
| 48 | 退出 | client-telemetry | `/metrics` **aborted** | — |

## 推荐阅读顺序

0. **登录/登出流程入门**：[`login/README.md`](login/README.md) —— 看 PKCE 怎么走、token 怎么签发/撤销/刷新。
1. **三条身份/启动**：[`docs/01`](docs/01-POST-prod.us-east-1.auth.desktop.kiro.dev_refreshToken.md)（Kiro refresh）→ [`docs/02`](docs/02-POST-cognito-identity.us-east-1.amazonaws.com_.md)（Cognito 换 STS）→ [`docs/03`](docs/03-POST-q.us-east-1.amazonaws.com_.md)（ListAvailableModels）
2. **业务核心**：[`docs/06`](docs/06-POST-q.us-east-1.amazonaws.com_.md)（首条 GenerateAssistantResponse）→ [`docs/07`](docs/07-POST-q.us-east-1.amazonaws.com_.md)（业务 telemetry）
3. **Toolkit telemetry 一条就够**：[`docs/04`](docs/04-POST-client-telemetry.us-east-1.amazonaws.com_metrics.md)
4. 其余按 idx 顺序看 —— 都是相同请求类的实例，差异主要是 conversationState.history 体积增长。

## 重新生成

```bash
# 业务流（crack/kiro/）
#   把新的 Whistle 全量 dump 放到 crack/raw/kiro-session-full.json
python3 crack/scripts/split.py kiro            # raw → rows/
python3 crack/scripts/sanitize.py              # 跨 crack/ 全量脱敏（幂等）
python3 crack/scripts/gen.py kiro              # rows → docs/
python3 crack/scripts/sanitize.py              # 再跑一次（gen 可能把 rows 里的明文搬到 docs）

# 登录/登出流程（crack/kiro/login/）
#   dump → crack/kiro/login/raw/kiro-login-session-full.json
#   若起始 rowId 变化，更新 crack/scripts/split.py 里 KIRO_LOGIN_START_ROWID
python3 crack/scripts/split.py kiro-login      # raw → rows/
python3 crack/scripts/sanitize.py
python3 crack/scripts/gen.py kiro-login        # rows → docs/
python3 crack/scripts/sanitize.py
```

## 与 Claude Code 抓包档案（`crack/oauth/` 等）的关系

| 维度 | Claude Code | Kiro CLI |
|---|---|---|
| 协议 | JSON 自定义 + SSE | AWS Smithy (`x-amz-json-1.0`/`1.1`) + event-stream |
| 鉴权 | OAuth Bearer / API Key 单轨 | Kiro Bearer + AWS SigV4 STS **双轨** |
| 业务 host | `api.anthropic.com` | `q.us-east-1.amazonaws.com` |
| Telemetry 通道 | Anthropic event_logging + Datadog public intake | CodeWhisperer SendTelemetryEvent + AWS toolkit-telemetry |
| 系统指令 | `system[]` 顶层数组 | 拼在 `userInputMessage.content` 内的 `--- CONTEXT ENTRY ---` 块 |
| 工具定义 | 客户端传 `tools[]` schema | 服务端按 `modelId` 内置，客户端只回 `toolResults` |
| Prompt cache | `cache_control.ephemeral.{ttl,scope}` 显式 | `promptCaching` 在 model 元信息里声明，请求里隐式管理 |

故 `crack/kiro/` 和 `crack/oauth/`、`crack/apikey/`、`crack/login/` 之间**没有可直接对比的 COMPARE.md** —— 它们描述的是完全不同的产品。
