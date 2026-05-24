#!/usr/bin/env python3
"""跨整个 crack/ 的统一脱敏脚本。

把抓包里的真实账号 / 会话 / token / STS 凭据值替换为固定占位符；再用一组
正则做兜底（CF cookie、未识别的 oauth token 长尾、裸主机名、AWS STS 等）。
幂等：在已经脱敏过的文件上再跑一次，输出 0 changed。

**重要：** 字面量替换映射存放在同目录的 `redaction_map.json`（gitignored）。
这是为了避免脚本自身把"待替换的真值"作为映射 key 暴露到公网 —— GitHub
secret-scanning 会正确地把它们识别为泄漏。

工作流：
    1. 抓新 session：dump 落到 crack/.../raw/*.json
    2. cp crack/scripts/redaction_map.example.json crack/scripts/redaction_map.json
    3. 把新 dump 里出现的真值填进 literals 字典
    4. python3 crack/scripts/sanitize.py   # 没有 map 文件会报错
    5. 提交 crack/.../{rows,docs} —— redaction_map.json 永远不进 git
"""
import json
import os
import re
import glob
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
CRACK_ROOT = os.path.dirname(HERE)
MAP_PATH = os.path.join(HERE, 'redaction_map.json')
EXAMPLE_PATH = os.path.join(HERE, 'redaction_map.example.json')


def load_literal_subs() -> dict:
    if not os.path.exists(MAP_PATH):
        sys.stderr.write(
            f'error: {os.path.relpath(MAP_PATH, CRACK_ROOT)} not found.\n'
            f'  This file holds the real-secret → placeholder map; it is gitignored.\n'
            f'  Copy {os.path.relpath(EXAMPLE_PATH, CRACK_ROOT)} to redaction_map.json\n'
            f'  and fill in the captured secrets before running sanitize.\n'
        )
        sys.exit(2)
    with open(MAP_PATH, 'r', encoding='utf-8') as f:
        data = json.load(f)
    subs = data.get('literals') or {}
    if not isinstance(subs, dict):
        sys.stderr.write(f'error: {MAP_PATH}: "literals" must be an object\n')
        sys.exit(2)
    return subs


# ---------- 正则兜底：与具体抓包无关的通用模式 ----------
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
    # -------- Kiro/Amazon-Q 抓包通用正则 --------
    # Kiro accessToken（形如 `aoaAAAAA...:base64sig`，~220 char）；幂等：跳过已替换
    (re.compile(r'aoaAAAAA(?!REDACTED)[A-Za-z0-9_+/\-]{20,}(?::[A-Za-z0-9_+/=\-]+)?'),
                                                                     'aoaAAAAAREDACTED_KIRO_ACCESS_TOKEN'),
    # Kiro refreshToken（`aorAAAAA...`）
    (re.compile(r'aorAAAAA(?!REDACTED)[A-Za-z0-9_+/\-]{20,}(?::[A-Za-z0-9_+/=\-]+)?'),
                                                                     'aorAAAAAREDACTED_KIRO_REFRESH_TOKEN'),
    # 任意 AWS STS AccessKeyId 残留（前缀 ASIA + 16 字母数字）
    (re.compile(r'ASIA(?!REDACTED)[A-Z0-9]{16}'),                    'ASIAREDACTEDAWSAKID0'),
    # AWS SessionToken（IQoJ 开头的 base64，~1500 char）；幂等
    (re.compile(r'IQoJb3JpZ2luX2Vj(?!_STS)[A-Za-z0-9+/=]{50,}'),     'IQoJb3JpZ2luX2Vj_STS_SESSION_TOKEN_REDACTED'),
    # SigV4 Authorization 里的 Signature=hex
    (re.compile(r'Signature=(?!REDACTED)[0-9a-f]{64}'),              'Signature=REDACTED_SIGNATURE_HEX_64'),
    # amz-sdk-invocation-id（每请求随机 uuid），仅在 header / amz-sdk-invocation-id JSON 字段里替换
    (re.compile(r'(amz-sdk-invocation-id["\s:=]+)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}',
                re.IGNORECASE),                                      r'\g<1>00000000-0000-0000-0000-000000000050'),
    # x-amz-security-token header 值（与 SessionToken 同源；冗余兜底，防止某些 header 截断的 IQoJ 前缀不完整）
    (re.compile(r'(x-amz-security-token["\s:=]+)(?!IQoJb3JpZ2luX2Vj_STS)[A-Za-z0-9+/=_\-]{200,}',
                re.IGNORECASE),                                      r'\g<1>IQoJb3JpZ2luX2Vj_STS_SESSION_TOKEN_REDACTED'),
]

# README 文件是手写文档，里面的"脱敏说明表"故意保留原始值用作映射查阅。
# 完全跳过自动脱敏 — 如果未来 README 写错引入了真敏感值，靠 audit 兜底，
# 而不是让 sanitize 去乱改散文。
SKIP_RELPATHS = {'README.md', 'login/README.md'}


def sanitize_text(text: str, literal_subs: dict) -> str:
    for old, new in literal_subs.items():
        text = text.replace(old, new)
    for pat, rep in REGEX_SUBS:
        text = pat.sub(rep, text)
    return text


def main() -> None:
    literal_subs = load_literal_subs()
    targets = []
    for pat in ('**/*.json', '**/*.md'):
        targets += glob.glob(os.path.join(CRACK_ROOT, pat), recursive=True)
    # exclude the redaction_map itself + anything in scripts/
    targets = sorted(set(
        p for p in targets
        if 'archive' not in p
        and not p.startswith(HERE + os.sep)
    ))

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
        new = sanitize_text(text, literal_subs)
        if new != text:
            with open(fn, 'w', encoding='utf-8') as f:
                f.write(new)
            changed += 1
            print(f'  redacted: {rel}')
    print(f'changed {changed}/{len(targets) - skipped} files (skipped {skipped} README, {len(literal_subs)} literals)')


if __name__ == '__main__':
    main()
