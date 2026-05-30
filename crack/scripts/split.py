#!/usr/bin/env python3
"""把 Kiro 抓包 raw dump 解码并拆成 crack/kiro/.../rows/NN-METHOD-host_path.json

用法：
    python3 crack/scripts/split.py kiro        # crack/kiro/raw/kiro-session-full.json → crack/kiro/rows/
    python3 crack/scripts/split.py kiro-login  # crack/kiro/login/raw/...            → crack/kiro/login/rows/

Claude 抓包走另一条链路（含会话私密内容），用 crack/scripts/extract_live.py 做
结构化脱敏，落到 crack/claude/。这里只处理 Kiro/Amazon-Q 抓包。
"""
import json, base64, os, gzip, subprocess, sys

HERE = os.path.dirname(os.path.abspath(__file__))
CRACK_ROOT = os.path.dirname(HERE)

VALID_MODES = ('kiro', 'kiro-login')

# kiro-login: 登录/登出 session 从这个 rowId 之后开始（按时间戳前缀过滤）。
# 重新抓包时只需更新这个常量。
KIRO_LOGIN_START_ROWID = '1779611075530'


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
    if mode == 'kiro-login':
        # 仅保留登录/登出 session 部分（按 rowId 时间戳前缀过滤）
        return [k for k in keys if k >= KIRO_LOGIN_START_ROWID]
    return keys


def main(mode: str) -> None:
    if mode not in VALID_MODES:
        sys.exit(f"unknown mode {mode!r}; expected one of {VALID_MODES}")

    if mode == 'kiro-login':
        src_path = os.path.join(CRACK_ROOT, 'kiro', 'login', 'raw', 'kiro-login-session-full.json')
        out_dir = os.path.join(CRACK_ROOT, 'kiro', 'login', 'rows')
    else:  # kiro
        src_path = os.path.join(CRACK_ROOT, 'kiro', 'raw', 'kiro-session-full.json')
        out_dir = os.path.join(CRACK_ROOT, 'kiro', 'rows')
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
    mode = sys.argv[1] if len(sys.argv) > 1 else 'kiro'
    main(mode)
