#!/usr/bin/env python3
"""把 crack/raw/{mode}-session-full.json 解码并拆成 crack/{mode}/rows/NN-METHOD-host_path.json

用法：
    python3 crack/scripts/split.py oauth     # → crack/oauth/rows/
    python3 crack/scripts/split.py apikey    # → crack/apikey/rows/
    python3 crack/scripts/split.py login     # → crack/login/rows/，仅保留登录链路相关请求

任何一个 mode 都从同一份脚本走，只在 login 模式开启额外的链路筛选。
"""
import json, base64, os, gzip, subprocess, sys

# 脚本固定锚定到 crack/ 根目录，跟工作目录无关
HERE = os.path.dirname(os.path.abspath(__file__))
CRACK_ROOT = os.path.dirname(HERE)

VALID_MODES = ('oauth', 'apikey', 'login')

# login 模式下保留的 host + path 前缀（其余视作业务/噪声）
LOGIN_HOSTS = (
    'api.anthropic.com',
    'platform.claude.com',
    'http-intake.logs.us5.datadoghq.com',
)
LOGIN_PATHS_HEAD = (
    '/api/hello',
    '/api/event_logging',
    '/api/v2/logs',
    '/v1/oauth/token',
    '/api/oauth/profile',
    '/api/oauth/claude_cli/roles',
    '/api/eval/sdk-',
    '/api/oauth/account/settings',
    '/api/claude_code_grove',
    '/api/claude_cli/bootstrap',
    '/api/claude_code_penguin_mode',
)


def maybe_decompress(raw: bytes, enc: str) -> bytes:
    if 'gzip' in enc:
        try:
            return gzip.decompress(raw)
        except Exception:
            return raw
    if 'br' in enc:
        p = subprocess.run(['brotli', '-d', '-c'], input=raw, capture_output=True)
        if p.returncode == 0:
            return p.stdout
        return raw
    return raw


def decode(rec):
    b64 = rec.get('base64') or ''
    if not b64:
        return None
    raw = base64.b64decode(b64)
    enc = (rec.get('headers') or {}).get('content-encoding', '')
    raw = maybe_decompress(raw, enc)
    try:
        return raw.decode('utf-8')
    except UnicodeDecodeError:
        return '[binary base64]: ' + base64.b64encode(raw).decode('ascii')


def select_keys(mode: str, rows_all: dict) -> list:
    keys = sorted(rows_all.keys())
    if mode != 'login':
        return keys
    # login: 从最早的 /api/hello 开始，到第一条业务 /v1/messages 之前；中间只保留登录链路相关 url
    start_key = next((k for k in keys if '/api/hello' in rows_all[k].get('url', '')), None)
    if start_key is None:
        sys.exit("未在 raw dump 里找到 /api/hello，无法确定登录起点")

    def is_login_url(url: str) -> bool:
        if not any(h in url for h in LOGIN_HOSTS):
            return False
        return any(p in url for p in LOGIN_PATHS_HEAD)

    out = []
    for k in keys:
        if k < start_key:
            continue
        url = rows_all[k].get('url', '')
        if '/v1/messages' in url and 'count_tokens' not in url:
            break
        if is_login_url(url):
            out.append(k)
    return out


def main(mode: str) -> None:
    if mode not in VALID_MODES:
        sys.exit(f"unknown mode {mode!r}; expected one of {VALID_MODES}")

    # oauth + apikey 共用 crack/raw/，login 走自己的 crack/login/raw/（数据布局沿袭旧版）
    if mode == 'login':
        src_path = os.path.join(CRACK_ROOT, 'login', 'raw', 'login-session-full.json')
    else:
        src_path = os.path.join(CRACK_ROOT, 'raw', f'{mode}-session-full.json')
    out_dir = os.path.join(CRACK_ROOT, mode, 'rows')
    os.makedirs(out_dir, exist_ok=True)

    src = json.load(open(src_path))
    rows_all = src['data']['data']
    selected = select_keys(mode, rows_all)

    manifest = []
    for i, k in enumerate(selected, 1):
        r = rows_all[k]
        method = r.get('req', {}).get('method', 'X')
        url = r.get('url', '')
        short = url.split('?', 1)[0].split('//', 1)[-1]
        safe = short.replace('/', '_').replace(':', '')[:80]
        fn = os.path.join(out_dir, f'{i:02d}-{method}-{safe}.json')
        out = {
            'idx': i,
            'rowId': k,
            'startTime': r.get('startTime'),
            'url': url,
            'method': method,
            'statusCode': r.get('res', {}).get('statusCode'),
            'reqHeaders': r.get('req', {}).get('headers'),
            'resHeaders': r.get('res', {}).get('headers'),
            'reqSize': r.get('req', {}).get('size'),
            'resSize': r.get('res', {}).get('size'),
            'reqBody': decode(r.get('req', {})),
            'resBody': decode(r.get('res', {})),
        }
        json.dump(out, open(fn, 'w'), indent=2, ensure_ascii=False)
        manifest.append({
            'idx': i, 'method': method, 'status': out['statusCode'], 'url': url,
            'file': os.path.relpath(fn, CRACK_ROOT),
            'reqBytes': len(out['reqBody']) if out['reqBody'] else 0,
            'resBytes': len(out['resBody']) if out['resBody'] else 0,
        })
    json.dump(manifest, open(os.path.join(out_dir, '_manifest.json'), 'w'), indent=2, ensure_ascii=False)
    for m in manifest:
        print(f"{m['idx']:2d} {m['method']:7s} {str(m['status']):14s} req={m['reqBytes']:>6d} res={m['resBytes']:>7d}  {m['url'][:90]}")


if __name__ == '__main__':
    mode = sys.argv[1] if len(sys.argv) > 1 else 'oauth'
    main(mode)
