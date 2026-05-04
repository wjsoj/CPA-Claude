#!/usr/bin/env python3
"""跨整个 crack/ 的补救脱敏脚本。修复 _sanitize.py 漏掉的几类残留：
  1. login docs/README 里残留的 OAuth state 值
  2. COMPARE.md 与 apikey/docs/14 里残留的截断 UUID
  3. apikey/oauth docs 中的 cf cookie (_cfuvid / __cf_bm)
  4. 多个文件里的裸 archpc 主机名
  5. login/raw 里历史会话残留的真实三方 apikey sk-T5j8tFqte...
"""
import os, re, glob

HERE = os.path.dirname(os.path.abspath(__file__))
os.chdir(HERE)

LITERAL_SUBS = {
    # OAuth state from this capture (43-char base64url, 32 bytes random)
    'RjeUoo1SwyOBY8gM-VwOp5MIu0YTShr1taUxf8pp9mo': 'OAUTH_STATE_REDACTED',
    # PKCE code_challenge from same capture
    'X8P9cgU16oMG6WbdwznwwVEaxFeaQ4m_lc61Bx4dUY0': 'CODE_CHALLENGE_REDACTED',
    # 真实账号/组织 UUID 的截断前缀
    '4fe8ffc6-...': '00000000-...',
    'dda51f19-...': '00000000-...',
    '4fe8ffc6':     '00000000',
    'dda51f19':     '00000000',
    # 真实三方 apikey (历史残留)
    'sk-T5j8tFqteGPBVrWqVFV1zeiutwSqp9wftPaR7i8': 'sk-REDACTED',
}

REGEX_SUBS = [
    # 任意未脱敏的 cf cookie
    (re.compile(r'_cfuvid=(?!REDACTED)[A-Za-z0-9._\-+/=]+'), '_cfuvid=REDACTED'),
    (re.compile(r'__cf_bm=(?!REDACTED)[A-Za-z0-9._\-+/=]+'), '__cf_bm=REDACTED'),
    # 兜底任何 sk-T5j8tFqte 长尾
    (re.compile(r'sk-T5j8tFqte[A-Za-z0-9_\-]+'), 'sk-REDACTED'),
    # 裸的 archpc 主机名（whistle 错误页 Host: archpc 等场景）
    (re.compile(r'\barchpc\b'), 'host'),
]

EXCLUDE = ('_sanitize_global.py', '_sanitize.py', 'README.md', 'login/README.md')

changed = 0
total = 0
for fn in sorted(glob.glob('**/*', recursive=True)):
    if not os.path.isfile(fn): continue
    if fn.endswith('.py'): continue
    if 'archive' in fn: continue
    total += 1
    # README 文件保留脱敏说明表里的原始值用于查阅 — 仅替换其中的 cookie 值与 state
    is_readme = fn in EXCLUDE
    with open(fn, 'rb') as f:
        raw = f.read()
    text = raw.decode('utf-8', errors='replace')
    new = text
    for old, repl in LITERAL_SUBS.items():
        if is_readme and old in ('4fe8ffc6', 'dda51f19', '4fe8ffc6-...', 'dda51f19-...', 'sk-T5j8tFqteGPBVrWqVFV1zeiutwSqp9wftPaR7i8'):
            # 这些字符串在脱敏说明表里有出现是必要的；跳过
            continue
        new = new.replace(old, repl)
    for pat, repl in REGEX_SUBS:
        new = pat.sub(repl, new)
    if new != text:
        with open(fn, 'w', encoding='utf-8') as f:
            f.write(new)
        changed += 1
        print(f'  redacted: {fn}')
print(f'changed {changed}/{total} files')
