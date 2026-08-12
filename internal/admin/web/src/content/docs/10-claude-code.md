---
slug: claude-code
title: Claude Code 接入
group: 客户端接入
order: 10
intro: 用两个环境变量把 Anthropic 官方 Claude Code CLI 指向 anthropic.phybench.cn。
---

## 一、端点信息

| 项 | 值 |
| --- | --- |
| 协议 | Anthropic Messages API |
| Base URL | `https://anthropic.phybench.cn`（**不要**加 `/v1`，也不要加结尾 `/`） |
| 环境变量 | `ANTHROPIC_BASE_URL` + `ANTHROPIC_AUTH_TOKEN` |
| 可用路由 | `POST /v1/messages`、`POST /v1/messages/count_tokens` |
| 密钥 | 控制台创建，形如 `sk-你的密钥` |

这个域名上**没有** `/v1/models`，请求它会返回 404。要列模型请用 Codex 域名。

## 二、安装

要求 Node.js **v18 及以上**（`node -v` 查看）。

```bash
npm install -g @anthropic-ai/claude-code
claude --version
```

Windows 原生 PowerShell 同样执行上面两条命令。

## 三、配置

用 `ANTHROPIC_AUTH_TOKEN`，不要用 `ANTHROPIC_API_KEY`：前者不会触发 Claude Code 对 `sk-ant-` 官方格式的校验。

### macOS / Linux

当前终端临时生效：

```bash
export ANTHROPIC_BASE_URL="https://anthropic.phybench.cn"
export ANTHROPIC_AUTH_TOKEN="sk-你的密钥"
```

永久生效（zsh 用 `~/.zshrc`，bash 把两处 `~/.zshrc` 换成 `~/.bashrc`）：

```bash
cat >> ~/.zshrc <<'EOF'
export ANTHROPIC_BASE_URL="https://anthropic.phybench.cn"
export ANTHROPIC_AUTH_TOKEN="sk-你的密钥"
EOF
source ~/.zshrc
```

### Windows（PowerShell）

当前窗口临时生效：

```powershell
$env:ANTHROPIC_BASE_URL = "https://anthropic.phybench.cn"
$env:ANTHROPIC_AUTH_TOKEN = "sk-你的密钥"
```

永久写入用户环境变量（执行一次，之后**重开** PowerShell 才生效）：

```powershell
[Environment]::SetEnvironmentVariable("ANTHROPIC_BASE_URL", "https://anthropic.phybench.cn", "User")
[Environment]::SetEnvironmentVariable("ANTHROPIC_AUTH_TOKEN", "sk-你的密钥", "User")
```

## 四、验证

1. `claude --version` 有版本号输出。提示 `command not found` 就重开终端窗口。
2. 确认变量已生效：macOS / Linux 执行 `echo $ANTHROPIC_BASE_URL`，Windows 执行 `echo $env:ANTHROPIC_BASE_URL`，应打印 `https://anthropic.phybench.cn`。
3. 发一次请求，看到回复即为接通；然后 `claude` 进入交互界面日常使用。

```bash
claude -p "只回复两个字：正常"
```

## 五、非交互 / 脚本调用

`-p` 表示 print 模式，跑完即退出，适合放进脚本和 CI。

```bash
# 一次性提问
claude -p "总结 README.md 的要点"

# 从管道读取输入
git diff | claude -p "用中文写一条 commit message"

# 指定模型 + 输出 JSON，便于程序解析
claude -p "列出本目录的构建命令" --model claude-sonnet-5 --output-format json
```

脚本里不要依赖 shell 配置文件，直接在命令前带上变量：

```bash
ANTHROPIC_BASE_URL="https://anthropic.phybench.cn" \
ANTHROPIC_AUTH_TOKEN="sk-你的密钥" \
claude -p "检查代码风格问题"
```

## 六、模型与切换

已定价的 Anthropic 模型：

```
claude-haiku-4-5-20251001   claude-haiku-4-5
claude-sonnet-4-6           claude-sonnet-5
claude-opus-4-6  claude-opus-4-7  claude-opus-4-8  claude-opus-5
claude-fable-5
```

切换方式：启动时 `claude --model claude-opus-5`，或会话中输入斜杠命令 `/model claude-sonnet-5`。

模型名可带后缀，会被正确识别并计费，例如 `claude-opus-5[1m]`（长上下文）。

## 七、直接调 API

本网关不做 User-Agent 限制，curl 与官方 SDK 都可以直连（`x-api-key: sk-你的密钥` 同样支持）：

```bash
curl https://anthropic.phybench.cn/v1/messages \
  -H "Authorization: Bearer sk-你的密钥" \
  -H "content-type: application/json" \
  -H "anthropic-version: 2023-06-01" \
  -d '{"model":"claude-sonnet-5","max_tokens":64,
       "messages":[{"role":"user","content":"你好"}]}'
```
