---
slug: codex-cli
title: Codex CLI 接入
group: 客户端接入
order: 11
intro: 写两个配置文件把 OpenAI 官方 Codex CLI 指向 openai.phybench.cn/v1，密钥与 Claude Code 通用。
---

## 一、端点信息

| 项 | 值 |
| --- | --- |
| 协议 | OpenAI Responses / Chat Completions API |
| Base URL | `https://openai.phybench.cn/v1`（**必须**带 `/v1`） |
| 可用路由 | `POST /v1/responses`、`POST /v1/responses/compact`、`POST /v1/chat/completions`、`GET /v1/models` |
| 密钥 | 与 Claude Code 同一个 `sk-你的密钥` |

漏掉 `/v1` 的直接表现就是 404。

## 二、安装

Codex CLI 要求 Node.js **v22 及以上**（Claude Code 只要 v18+）。低于 v22 先用 `nvm install 22 && nvm use 22` 升级。

```bash
node -v                      # 应为 v22 或更高
npm install -g @openai/codex
codex --version
```

Windows 用户如果在原生 PowerShell 遇到 shell / PTY 问题，改用 WSL2（`wsl --install`）后在 Ubuntu 里执行上面的命令。

## 三、配置（推荐：配置文件）

配置目录：macOS / Linux 是 `~/.codex/`，Windows 是 `%USERPROFILE%\.codex\`。需要两个文件。

`~/.codex/config.toml` 完整内容：

```toml
model_provider = "cpa"
model = "gpt-5.5"
model_reasoning_effort = "high"
disable_response_storage = true

[model_providers.cpa]
name = "CPA-Claude"
base_url = "https://openai.phybench.cn/v1"
wire_api = "responses"
requires_openai_auth = true
```

`~/.codex/auth.json` 完整内容：

```json
{ "OPENAI_API_KEY": "sk-你的密钥" }
```

`disable_response_storage = true` 必须保留：第三方网关不提供 OpenAI 的 response 存储。

### macOS / Linux 建目录并打开编辑器

把上面两段内容分别粘进去保存：

```bash
mkdir -p ~/.codex
nano ~/.codex/config.toml
nano ~/.codex/auth.json
chmod 600 ~/.codex/auth.json
```

### Windows（PowerShell）

```powershell
New-Item -ItemType Directory -Force "$env:USERPROFILE\.codex" | Out-Null
notepad "$env:USERPROFILE\.codex\config.toml"
notepad "$env:USERPROFILE\.codex\auth.json"
```

记事本保存时编码选 UTF-8，注意文件名别被加上 `.txt` 后缀。

## 四、环境变量方式（备选）

仅适合临时调试，`wire_api` 等设置仍然要靠 `config.toml`。

macOS / Linux：

```bash
export OPENAI_BASE_URL="https://openai.phybench.cn/v1"
export OPENAI_API_KEY="sk-你的密钥"
```

Windows PowerShell（临时；把 `$env:X = "y"` 换成 `[Environment]::SetEnvironmentVariable("X", "y", "User")` 即永久写入）：

```powershell
$env:OPENAI_BASE_URL = "https://openai.phybench.cn/v1"
$env:OPENAI_API_KEY = "sk-你的密钥"
```

## 五、验证

1. `node -v` ≥ v22，`codex --version` 有输出。
2. 用 curl 验密钥和路由（本网关不限制 User-Agent）。返回模型列表即正常；401 是密钥不对，404 是 URL 漏了 `/v1`。
3. 再跑一次真实请求，成功后用 `codex` 进入交互界面。

```bash
curl -s https://openai.phybench.cn/v1/models -H "Authorization: Bearer sk-你的密钥"
codex exec "只回复两个字：正常"
```

## 六、非交互 / 脚本调用

`codex exec` 跑完即退出，适合放进脚本和 CI：

```bash
codex exec "为 src/ 下的函数补充类型注解"
codex exec --model gpt-5.3-codex "解释这个仓库的构建流程"
```

## 七、模型与推理强度

`GET /v1/models` 返回当前实际可用集合。常见模型名：

```
gpt-5.6-sol  gpt-5.6-terra  gpt-5.6-luna
gpt-5.5  gpt-5.4  gpt-5.4-mini  gpt-5.2
gpt-5.3-codex  gpt-5.3-codex-spark
gpt-5  gpt-5-mini  gpt-5-nano  gpt-4o  gpt-4o-mini
```

切换模型：改 `config.toml` 里的 `model =`，或临时 `codex --model gpt-5.4`。

`model_reasoning_effort` 可选值：

| 值 | 含义 |
| --- | --- |
| `minimal` | 几乎不推理，最快最省 |
| `low` | 轻量推理，适合简单改动 |
| `medium` | 默认平衡档 |
| `high` | 最深推理，复杂重构用 |

模型名也可带推理后缀，会被正确识别并计费，例如 `gpt-5.3-codex(high)`。
