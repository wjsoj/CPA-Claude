#!/usr/bin/env python3
"""把 raw/{mode}-session-full.json 解码并拆成 {mode}/rows/NN-METHOD-host_path.json"""
import json, base64, os, gzip, subprocess, sys

mode = sys.argv[1] if len(sys.argv) > 1 else 'oauth'
src_path = f'raw/{mode}-session-full.json'
out_dir = f'{mode}/rows'
os.makedirs(out_dir, exist_ok=True)

src = json.load(open(src_path))
rows = src['data']['data']
keys = sorted(rows.keys())

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
for i, k in enumerate(keys, 1):
    r = rows[k]
    method = r.get('req',{}).get('method','X')
    url = r.get('url','')
    short = url.split('?',1)[0].split('//',1)[-1]
    safe = short.replace('/','_').replace(':','')[:80]
    fn = f'{out_dir}/{i:02d}-{method}-{safe}.json'
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
    manifest.append({'idx':i,'method':method,'status':out['statusCode'],'url':url,'file':fn,
                     'reqBytes': len(out['reqBody']) if out['reqBody'] else 0,
                     'resBytes': len(out['resBody']) if out['resBody'] else 0})
json.dump(manifest, open(f'{out_dir}/_manifest.json','w'), indent=2, ensure_ascii=False)
for m in manifest:
    print(f"{m['idx']:2d} {m['method']:7s} {str(m['status']):14s} req={m['reqBytes']:>6d} res={m['resBytes']:>7d}  {m['url'][:90]}")
