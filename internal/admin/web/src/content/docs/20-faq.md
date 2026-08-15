---
slug: faq
title: 常见问题
group: 参考
order: 20
intro: 报错码对照表，以及两个域名、密钥通用性、直连限制的几条速查答案。
---

## 报错速查

| 状态码 | 原因 | 怎么修 |
| --- | --- | --- |
| `401` | 没带密钥 / 密钥写错 / 复制时混进空格换行 / 鉴权头拼错 | 重新完整复制密钥，用下面的 curl 单独验 |
| `404` | Codex 的 `base_url` 漏了 `/v1`；或往 anthropic 域名请求了 `/v1/models` | 对照下面的域名表改 base_url |
| `503` | 上游号池当下没有可用凭证 | 读响应体里的原因；按 `Retry-After` 等待重试，该值最长 300 秒 |
| `402` | 账户余额不足（仅计费开启时出现） | 在控制台充值后重试 |

### 401 怎么单独验密钥

输出 `200` 说明密钥没问题，再回头查客户端配置。

```bash
curl -s -o /dev/null -w '%{http_code}\n' https://openai.phybench.cn/v1/models -H "Authorization: Bearer sk-你的密钥"
```

### 404 怎么改

```bash
# Codex 的 config.toml：必须带 /v1
base_url = "https://openai.phybench.cn/v1"

# Claude Code：必须不带 /v1
export ANTHROPIC_BASE_URL="https://anthropic.phybench.cn"
```

### 403 client_not_allowed

本网关**不会**返回这个错误。收到它说明请求实际发到了别的网关（某些网关会按 User-Agent 拦截脚本类客户端）——请检查 base_url 是不是写成了其他地址。

## 两个域名怎么记

| 客户端 | 值 | 记法 |
| --- | --- | --- |
| Claude Code | `https://anthropic.phybench.cn` | Anthropic 协议，**不带** `/v1`，客户端自己拼 `/v1/messages` |
| Codex CLI | `https://openai.phybench.cn/v1` | OpenAI 协议，**必须带** `/v1` |

一句话：**anthropic 不带，openai 带。**

## 一个密钥能两边用吗

能。同一个 `sk-你的密钥` 在两个域名上都有效，不用分别申请。

## 鉴权头写哪个

`Authorization: Bearer sk-你的密钥` 和 `x-api-key: sk-你的密钥` 都支持，服务端先看前者。`Authorization` 若没写 `Bearer ` 前缀，整个头的值会被当作密钥原样使用。

## 怎么列出可用模型

只有 openai 域名提供 `GET /v1/models`，anthropic 域名上请求它会 404。Anthropic 侧不做模型白名单，任何 model 字符串都会转发到上游，未列入价目表的按默认价计费。

## curl 和 SDK 能直连吗

能。本网关**没有 User-Agent 拦截**，curl、官方 anthropic / openai SDK、LiteLLM、自写脚本都可直接请求两个端点。

## 状态页在哪

`https://anthropic.phybench.cn/status/`。只在 anthropic 域名上，openai 域名访问 `/status/` 会 404。

## 报销需要消费单据怎么办

状态页 `Usage lookup` 标签页 → 找到你的密钥 → 请求明细右上角的 **对账单** 按钮，选好时间区间即可导出 PDF。

弹窗里会先显示该区间的真实消费（人民币）和该密钥的累计消费，确认无误再下载。PDF 含逐笔调用明细、按模型汇总、合计金额，以及换算所用的美元汇率与取数时间——金额可复核。

注意这是**用量对账单，不是发票**。两者用途不同：

| | 对账单 | 发票 |
| --- | --- | --- |
| 金额依据 | 实际消费 | 实际充值 |
| 从哪里开 | 状态页导出，即时 | 钱包页申请，人工开具后邮件发送 |

如果你充了 500 元只用掉 300 元，要按 500 元报销，那应该开**发票**——发票是按充值金额开的。对账单只能如实反映用掉的那部分。
