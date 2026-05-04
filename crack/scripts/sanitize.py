#!/usr/bin/env python3
"""跨整个 crack/ 的统一脱敏脚本。

合并了之前分散的 _sanitize.py + _sanitize_global.py + login/_sanitize.py 三段
逻辑：把所有"原始本机 / 账号 / 浏览器会话敏感值"按固定占位符表替换，再用一组
正则做兜底（CF cookie、未识别的 oauth token 长尾、裸主机名等）。

幂等：在已经脱敏过的文件上再跑一次，输出 0 changed。

用法：
    python3 crack/scripts/sanitize.py
"""
import os
import re
import glob

HERE = os.path.dirname(os.path.abspath(__file__))
CRACK_ROOT = os.path.dirname(HERE)

# ---------- 字符串字面替换：来自本次抓包的真实账号 / 会话值 ----------
LITERAL_SUBS = {
    # access / refresh tokens（完整长度，新登录抓到的那一对）
    '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': 'sk-ant-oat01-REDACTED',
    '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': 'sk-ant-ort01-REDACTED',
    # 真实三方 apikey（历史残留在 oauth/apikey/login/raw 里）
    'sk-T5j8tFqteGPBVrWqVFV1zeiutwSqp9wftPaR7i8':                    'sk-REDACTED',
    # uuid / 邮箱 / 用户名 / device_id
    'ccd4b34d-b955-4435-af57-bf1bbeba91e7':                          '00000000-0000-0000-0000-000000000003',
    '4fe8ffc6-4b58-4454-859d-1a6aa823154b':                          '00000000-0000-0000-0000-000000000001',
    'dda51f19-a74e-4372-bc86-218118aff6e2':                          '00000000-0000-0000-0000-000000000002',
    'bcd78271-3d93-47ec-bd1f-295c22d52b10':                          '00000000-0000-0000-0000-000000000010',
    '1225ef802a7a88454489035a63d1966e11f2ba2065128262b7ff8ca3cd9afe0b': '0' * 64,
    'miara_bernu867@mail.com':                                       'redacted@example.com',
    'Noah':                                                          'REDACTED_USER',
    # OAuth 一次性参数
    '4yeQmwf3clQIziavZ6HztbPk6ImsGXSIrAQTBOXzZzOfAZTQ':              'OAUTH_CODE_REDACTED',
    'cisycjrl7qZ7sWbxbM4GiS5TiEssw-N5FqbdhOHypjc':                   'CODE_VERIFIER_REDACTED',
    'RjeUoo1SwyOBY8gM-VwOp5MIu0YTShr1taUxf8pp9mo':                   'OAUTH_STATE_REDACTED',
    'X8P9cgU16oMG6WbdwznwwVEaxFeaQ4m_lc61Bx4dUY0':                   'CODE_CHALLENGE_REDACTED',
    # uuid 截断前缀（在 docs / COMPARE.md 里出现的"4fe8ffc6-..."形式）
    '4fe8ffc6-...':                                                  '00000000-...',
    'dda51f19-...':                                                  '00000000-...',
    # 单独 8-char 前缀（出现在比较表 anthropic-organization-id: dda51f19-... 等位置）
    '4fe8ffc6':                                                      '00000000',
    'dda51f19':                                                      '00000000',
}

# ---------- 正则兜底：覆盖未在名单内的 token / cookie / 裸主机名等 ----------
REGEX_SUBS = [
    # 任意 oauth bearer / refresh token 残留
    (re.compile(r'sk-ant-oat01-(?!REDACTED)[A-Za-z0-9_\-]{20,}'),    'sk-ant-oat01-REDACTED'),
    (re.compile(r'sk-ant-ort01-(?!REDACTED)[A-Za-z0-9_\-]{20,}'),    'sk-ant-ort01-REDACTED'),
    # 任意三方 apikey 长尾
    (re.compile(r'sk-T5j8tFqte[A-Za-z0-9_\-]+'),                     'sk-REDACTED'),
    # CF cookies — 注意排除已经替换为 REDACTED 的项以保证幂等
    (re.compile(r'__cf_bm=(?!REDACTED)[A-Za-z0-9._\-+/=]+'),         '__cf_bm=REDACTED'),
    (re.compile(r'_cfuvid=(?!REDACTED)[A-Za-z0-9._\-+/=]+'),         '_cfuvid=REDACTED'),
    # cf-ray / request-id（同时支持裸 JSON 和转义后的字符串）
    (re.compile(r'\\?"cf-ray\\?"\s*:\s*\\?"[^"\\]+\\?"'),            r'"cf-ray": "REDACTED-cf-ray"'),
    (re.compile(r'\\?"request-id\\?"\s*:\s*\\?"[^"\\]+\\?"'),        r'"request-id": "req_REDACTED"'),
    # 主机用户名 / 路径
    (re.compile(r'/home/wjs/'),                                      '/home/user/'),
    (re.compile(r'\bwjs\b'),                                         'user'),
    # Linux 内核 / 发行版 / 终端 / 主机名
    (re.compile(r'7\.0\.3-arch1-1'),                                 '6.10.0-generic'),
    (re.compile(r'(\\?")konsole(\\?")'),                             r'\1xterm\2'),
    (re.compile(r'(linux_distro_id\\?"\s*:\s*\\?")arch(\\?")'),      r'\1generic\2'),
    (re.compile(r'(\\?")archpc(\\?")'),                              r'\1host\2'),
    (re.compile(r'\barchpc\b'),                                      'host'),
    # LAN IP
    (re.compile(r'10\.3\.31\.133'),                                  '10.0.0.10'),
    (re.compile(r'10\.129\.81\.88'),                                 '10.0.0.20'),
]

# README 文件是手写文档，里面的"脱敏说明表"故意保留原始值用作映射查阅。
# 完全跳过自动脱敏 — 如果未来 README 写错引入了真敏感值，靠 audit 兜底，
# 而不是让 sanitize 去乱改散文。
SKIP_RELPATHS = {'README.md', 'login/README.md'}


def sanitize_text(text: str) -> str:
    for old, new in LITERAL_SUBS.items():
        text = text.replace(old, new)
    for pat, rep in REGEX_SUBS:
        text = pat.sub(rep, text)
    return text


def main() -> None:
    targets = []
    for pat in ('**/*.json', '**/*.md'):
        targets += glob.glob(os.path.join(CRACK_ROOT, pat), recursive=True)
    targets = sorted(set(p for p in targets if 'archive' not in p))

    changed = 0
    skipped = 0
    for fn in targets:
        rel = os.path.relpath(fn, CRACK_ROOT)
        if rel in SKIP_RELPATHS:
            skipped += 1
            continue
        try:
            text = open(fn, 'rb').read().decode('utf-8', errors='replace')
        except OSError:
            continue
        new = sanitize_text(text)
        if new != text:
            with open(fn, 'w', encoding='utf-8') as f:
                f.write(new)
            changed += 1
            print(f'  redacted: {rel}')
    print(f'changed {changed}/{len(targets) - skipped} files (skipped {skipped} README)')


if __name__ == '__main__':
    main()
