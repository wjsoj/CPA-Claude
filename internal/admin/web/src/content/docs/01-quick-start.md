---
slug: quick-start
title: 快速开始
group: 开始
order: 1
intro: 两个接入域名、一个密钥，三分钟把 Claude Code 或 Codex CLI 指向本网关。
---

## 一、两个域名（最容易搞错的一步）

| 客户端 | 配置项 | 值 |
| --- | --- | --- |
| Claude Code | `ANTHROPIC_BASE_URL` | `https://anthropic.phybench.cn` |
| Codex CLI | `base_url` / `OPENAI_BASE_URL` | `https://openai.phybench.cn/v1` |

记法：Anthropic 域名**不带** `/v1`（客户端自己会拼 `/v1/messages`）；OpenAI 域名**必须带** `/v1`。写反了会 404。

## 二、我该用哪个客户端

- 写代码、要 MCP / 子 agent / 提示缓存 → **Claude Code**，走 anthropic 域名。
- 想用 GPT-5 系列模型、习惯 OpenAI 协议 → **Codex CLI**，走 openai 域名。
- 两个都想用：装两个客户端即可，**同一个密钥两边通用**。

## 三、拿到密钥

在管理控制台的密钥页面创建一个客户端密钥并完整复制。格式是 `sk-` 加 48 位字母数字，本文里统一写作 `sk-你的密钥`。复制时别带首尾空格或换行，粘错会直接 401。

## 四、最短接入路径

### Claude Code

```bash
npm install -g @anthropic-ai/claude-code
export ANTHROPIC_BASE_URL="https://anthropic.phybench.cn"
export ANTHROPIC_AUTH_TOKEN="sk-你的密钥"
claude
```

详细步骤见「Claude Code 接入」。

### Codex CLI

```bash
npm install -g @openai/codex
mkdir -p ~/.codex
cat > ~/.codex/auth.json <<'JSON'
{ "OPENAI_API_KEY": "sk-你的密钥" }
JSON
```

还需要写 `~/.codex/config.toml`，完整内容见「Codex CLI 接入」。

## 五、验证网关可达

```bash
curl -s https://openai.phybench.cn/v1/models \
  -H "Authorization: Bearer sk-你的密钥"
```

返回模型列表即为正常。本网关**不限制 User-Agent**，curl、官方 SDK、脚本都能直连。状态页在 `https://anthropic.phybench.cn/status/`。
