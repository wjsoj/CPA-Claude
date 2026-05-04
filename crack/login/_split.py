#!/usr/bin/env python3
"""把 raw/login-session-full.json 解码并拆成 rows/NN-METHOD-host_path.json
只保留登录链路相关的请求，按 startTime 升序。"""
import json, base64, os, gzip, subprocess, sys

HERE = os.path.dirname(os.path.abspath(__file__))
src_path = os.path.join(HERE, 'raw/login-session-full.json')
out_dir = os.path.join(HERE, 'rows')
os.makedirs(out_dir, exist_ok=True)

src = json.load(open(src_path))
rows_all = src['data']['data']

# 只保留登录链路：从 /api/hello 开始 到 /api/claude_code_penguin_mode 截止
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

def is_login(url):
    if not any(h in url for h in LOGIN_HOSTS):
        return False
    return any(p in url for p in LOGIN_PATHS_HEAD)

# 取最早出现的 /api/hello 作为登录起点
keys = sorted(rows_all.keys())
start_key = None
for k in keys:
    if '/api/hello' in rows_all[k].get('url',''):
        start_key = k
        break
if not start_key:
    print('未找到 /api/hello，退出'); sys.exit(1)

# 取该起点之后、且属于登录路径的，直到第一个 v1/messages 之前
selected = []
seen_penguin = False
for k in keys:
    if k < start_key: continue
    r = rows_all[k]
    url = r.get('url','')
    if '/v1/messages' in url and 'count_tokens' not in url:
        break  # 业务消息开始，登录链路结束
    if is_login(url):
        selected.append(k)

def maybe_decompress(raw, enc):
    if 'gzip' in enc:
        try: return gzip.decompress(raw)
        except: return raw
    if 'br' in enc:
        p = subprocess.run(['brotli','-d','-c'], input=raw, capture_output=True)
        if p.returncode == 0: return p.stdout
        return raw
    return raw

def decode(rec):
    b64 = rec.get('base64') or ''
    if not b64: return None
    raw = base64.b64decode(b64)
    enc = (rec.get('headers') or {}).get('content-encoding','')
    raw = maybe_decompress(raw, enc)
    try: return raw.decode('utf-8')
    except: return '[binary base64]: ' + base64.b64encode(raw).decode('ascii')

manifest = []
for i, k in enumerate(selected, 1):
    r = rows_all[k]
    method = r.get('req',{}).get('method','X')
    url = r.get('url','')
    short = url.split('?',1)[0].split('//',1)[-1]
    safe = short.replace('/','_').replace(':','')[:80]
    fn = os.path.join(out_dir, f'{i:02d}-{method}-{safe}.json')
    out = {
        'idx': i,
        'rowId': k,
        'startTime': r.get('startTime'),
        'url': url,
        'method': method,
        'statusCode': r.get('res',{}).get('statusCode'),
        'reqHeaders': r.get('req',{}).get('headers'),
        'resHeaders': r.get('res',{}).get('headers'),
        'reqSize': r.get('req',{}).get('size'),
        'resSize': r.get('res',{}).get('size'),
        'reqBody': decode(r.get('req',{})),
        'resBody': decode(r.get('res',{})),
    }
    json.dump(out, open(fn,'w'), indent=2, ensure_ascii=False)
    manifest.append({'idx':i,'method':method,'status':out['statusCode'],'url':url,'file':os.path.relpath(fn,HERE),
                     'reqBytes': len(out['reqBody']) if out['reqBody'] else 0,
                     'resBytes': len(out['resBody']) if out['resBody'] else 0})
json.dump(manifest, open(os.path.join(out_dir,'_manifest.json'),'w'), indent=2, ensure_ascii=False)
for m in manifest:
    print(f"{m['idx']:2d} {m['method']:7s} {str(m['status']):14s} req={m['reqBytes']:>6d} res={m['resBytes']:>7d}  {m['url'][:90]}")
