#!/usr/bin/env python3
"""脱敏 crack/login/ 下所有 raw / rows 文件，统一替换为占位符。"""
import os, re, glob, json

HERE = os.path.dirname(os.path.abspath(__file__))

# 命名占位符
PLACE = {
    'access_token_prefix':  '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',
    'refresh_token':        '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',
    'token_uuid':           'ccd4b34d-b955-4435-af57-bf1bbeba91e7',
    'account_uuid':         '4fe8ffc6-4b58-4454-859d-1a6aa823154b',
    'org_uuid':             'dda51f19-a74e-4372-bc86-218118aff6e2',
    'email':                'miara_bernu867@mail.com',
    'full_name':            'Noah',
    'session_id':           'bcd78271-3d93-47ec-bd1f-295c22d52b10',
    'device_id':            '1225ef802a7a88454489035a63d1966e11f2ba2065128262b7ff8ca3cd9afe0b',
    'oauth_code':           '4yeQmwf3clQIziavZ6HztbPk6ImsGXSIrAQTBOXzZzOfAZTQ',
    'code_verifier':        'cisycjrl7qZ7sWbxbM4GiS5TiEssw-N5FqbdhOHypjc',
    'state':                'RjeUoo1SwyOBY8gM-VwOp5MIu0YTShr1taUxf8pp9mo',
}
SUB = {
    PLACE['access_token_prefix']: 'sk-ant-oat01-REDACTED',
    PLACE['refresh_token']:       'sk-ant-ort01-REDACTED',
    PLACE['token_uuid']:          '00000000-0000-0000-0000-000000000003',
    PLACE['account_uuid']:        '00000000-0000-0000-0000-000000000001',
    PLACE['org_uuid']:            '00000000-0000-0000-0000-000000000002',
    PLACE['email']:               'redacted@example.com',
    PLACE['full_name']:           'REDACTED_USER',
    PLACE['session_id']:          '00000000-0000-0000-0000-000000000010',
    PLACE['device_id']:           '0' * 64,
    PLACE['oauth_code']:          'OAUTH_CODE_REDACTED',
    PLACE['code_verifier']:       'CODE_VERIFIER_REDACTED',
    PLACE['state']:               'OAUTH_STATE_REDACTED',
}

# Cloudflare cookies / cf-ray / request-id / cookies — 用正则覆盖
REGEX_SUBS = [
    # 匹配残留的（不在已知账号名单里的）oauth bearer 与 refresh token
    (re.compile(r'sk-ant-oat01-[A-Za-z0-9_\-]{20,}'), 'sk-ant-oat01-REDACTED'),
    (re.compile(r'sk-ant-ort01-[A-Za-z0-9_\-]{20,}'), 'sk-ant-ort01-REDACTED'),
    (re.compile(r'__cf_bm=[A-Za-z0-9._\-+/=]+'),      '__cf_bm=REDACTED'),
    (re.compile(r'_cfuvid=[A-Za-z0-9._\-+/=]+'),      '_cfuvid=REDACTED'),
    (re.compile(r'\\?"cf-ray\\?"\s*:\s*\\?"[^"\\]+\\?"'),    r'"cf-ray": "REDACTED-cf-ray"'),
    (re.compile(r'\\?"request-id\\?"\s*:\s*\\?"[^"\\]+\\?"'),r'"request-id": "req_REDACTED"'),
    # 用户名/路径
    (re.compile(r'/home/wjs/'),                       '/home/user/'),
    (re.compile(r'\bwjs\b'),                          'user'),
    (re.compile(r'7\.0\.3-arch1-1'),                  '6.10.0-generic'),
    # konsole / arch 同时支持 "x" 与转义后的 \"x\"
    (re.compile(r'(\\?")konsole(\\?")'),              r'\1xterm\2'),
    (re.compile(r'(linux_distro_id\\?"\s*:\s*\\?")arch(\\?")'), r'\1generic\2'),
    # archpc 主机名（也可能在转义里或裸出现在 Host: header）
    (re.compile(r'(\\?")archpc(\\?")'),               r'\1host\2'),
    (re.compile(r'\barchpc\b'),                       'host'),
    # LAN IPs
    (re.compile(r'10\.3\.31\.133'),                   '10.0.0.10'),
    (re.compile(r'10\.129\.81\.88'),                  '10.0.0.20'),
]

def sanitize_text(text: str) -> str:
    for old, new in SUB.items():
        text = text.replace(old, new)
    for pat, rep in REGEX_SUBS:
        text = pat.sub(rep, text)
    return text

targets = []
targets += glob.glob(os.path.join(HERE, 'rows/*.json'))
targets += glob.glob(os.path.join(HERE, 'raw/*.json'))

changed = 0
for fn in targets:
    with open(fn, 'rb') as f:
        raw = f.read()
    text = raw.decode('utf-8', errors='replace')
    new = sanitize_text(text)
    if new != text:
        with open(fn, 'w', encoding='utf-8') as f:
            f.write(new)
        changed += 1
        print(f'  redacted: {os.path.relpath(fn, HERE)}')
print(f'changed {changed}/{len(targets)} files')
