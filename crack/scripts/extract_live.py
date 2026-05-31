#!/usr/bin/env python3
"""Structurally-redacting extractor for a LIVE Claude Code session capture.

Unlike split.py (which keeps bodies verbatim and relies on sanitize.py's
literal/regex map), this script is for dumps of a *real working session* whose
request bodies contain private conversation content. It walks each JSON body and
keeps only the fingerprint-bearing STRUCTURE — keys, block types, cache_control,
versions, betas, env, metadata shape — replacing free-text prose, code, tool
descriptions, and identity values with `<redacted …>` placeholders.

Usage:
    python3 crack/scripts/extract_live.py /path/to/whistle-dump.json [outdir]

Default outdir = crack/claude/rows/. The source dump is NOT copied or committed.
"""
import json, base64, gzip, subprocess, sys, os, collections, re

HERE = os.path.dirname(os.path.abspath(__file__))
CRACK_ROOT = os.path.dirname(HERE)

MASK_KEYS = {"device_id", "account_uuid", "organization_uuid", "email",
             "session_id", "user_id", "event_id", "rh", "previous_message_id"}
MASK_HEADERS = {"authorization", "x-api-key", "cookie", "set-cookie",
                "x-claude-code-session-id", "x-client-request-id", "request-id",
                "x-organization-uuid"}
# Secret / identity values carried in OAuth login bodies. Masked by value (not
# structure) so the request-param NAMES, response KEYS, and non-secret
# fingerprint values (scope, token_type, expires_in, has_claude_max,
# organization_type, rate_limit_tier, billing_type, …) stay verbatim. The
# public Claude Code client_id lives under the `application` subtree and is
# deliberately kept — it is a known constant, not a secret.
OAUTH_MASK_KEYS = {"code", "code_verifier", "code_challenge", "state",
                   "access_token", "refresh_token", "token_uuid",
                   "uuid", "name", "email", "email_address",
                   "full_name", "display_name", "created_at",
                   "subscription_created_at",
                   # camelCase / snake variants seen in startup bodies
                   "organization_uuid", "organizationuuid", "account_uuid",
                   "accountuuid", "organization_name", "device_id",
                   "deviceid", "session_id", "sessionid", "user_id",
                   "userid", "id"}


def _normkey(k):
    return k.lower().replace("_", "").replace("-", "") if isinstance(k, str) else k


_OAUTH_MASK_NORM = {_normkey(k) for k in OAUTH_MASK_KEYS}

# Universal identity scrub — applied to EVERY emitted row (headers, url, bodies)
# as defense-in-depth, since sanitize.py's literal map does not cover generic
# UUID / email / device-hash patterns. The public Claude Code client_id is a
# documented constant and is deliberately preserved.
KEEP_UUIDS = {"9d1c250a-e61b-44d9-88ed-5944d1962f5e"}
UUID_RE = re.compile(
    r"\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b", re.I)
EMAIL_RE = re.compile(r"[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}")
SHA256_RE = re.compile(r"\b[0-9a-f]{64}\b", re.I)


def _scrub_str(s):
    s = UUID_RE.sub(lambda m: m.group(0) if m.group(0).lower() in KEEP_UUIDS
                    else "<masked:uuid>", s)
    s = EMAIL_RE.sub("<masked:email>", s)
    s = SHA256_RE.sub("<masked:hash>", s)
    return s


def scrub_identity(o):
    if isinstance(o, dict):
        return {k: scrub_identity(v) for k, v in o.items()}
    if isinstance(o, list):
        return [scrub_identity(x) for x in o]
    if isinstance(o, str):
        return _scrub_str(o)
    return o
KEEP_TEXT_PREFIXES = ("x-anthropic-billing-header:",
                      "You are Claude Code, Anthropic's official CLI for Claude.")
TEXT_LIMIT = 80


def decompress(raw, enc):
    if "gzip" in enc:
        try: return gzip.decompress(raw)
        except Exception: return raw
    if "br" in enc:
        p = subprocess.run(["brotli", "-d", "-c"], input=raw, capture_output=True)
        return p.stdout if p.returncode == 0 else raw
    return raw


def body_bytes(rec):
    b64 = rec.get("base64") or ""
    if not b64: return None
    raw = base64.b64decode(b64)
    return decompress(raw, (rec.get("headers") or {}).get("content-encoding", ""))


def redact(o, key=None):
    """Recursively keep structure, redact prose + identity values."""
    if isinstance(o, dict):
        return {k: redact(v, k) for k, v in o.items()}
    if isinstance(o, list):
        # collapse long homogeneous lists (messages, tools, events) to a marker
        if len(o) > 6 and all(isinstance(x, (dict, str)) for x in o):
            head = [redact(x) for x in o[:2]]
            return head + [f"<… {len(o) - 3} more items redacted …>", redact(o[-1])]
        return [redact(x) for x in o]
    if isinstance(o, str):
        if key in MASK_KEYS:
            return f"<masked:{key}>"
        if any(o.startswith(p) for p in KEEP_TEXT_PREFIXES):
            return o  # fingerprint-bearing — keep verbatim
        if len(o) > TEXT_LIMIT:
            return f"<text:{len(o)} chars>"
        return o
    return o


def redact_user_id(body_obj):
    """metadata.user_id is a JSON STRING; redact its inner identity fields."""
    md = body_obj.get("metadata")
    if isinstance(md, dict) and isinstance(md.get("user_id"), str):
        try:
            inner = json.loads(md["user_id"])
            md["user_id"] = json.dumps({k: f"<masked:{k}>" for k in inner})
        except Exception:
            md["user_id"] = "<masked:user_id>"


def redact_oauth(o, parent=None):
    """Keep OAuth body structure; mask secret/identity VALUES by key. The
    `application` subtree (uuid/name/slug = public Claude Code app identity)
    is kept verbatim — it is the same constant baked into our code."""
    if isinstance(o, dict):
        return {k: (redact_oauth(v, k) if k != "application" else v)
                for k, v in o.items()}
    if isinstance(o, list):
        return [redact_oauth(x, parent) for x in o]
    if isinstance(o, str) and _normkey(parent) in _OAUTH_MASK_NORM:
        return f"<masked:{parent}>"
    return o


def summarize_oauth(text):
    """Body summarizer for OAuth login / startup endpoints: parse JSON, mask
    secret values, keep the rest verbatim (login bodies are small and every
    non-secret field is fingerprint-bearing). Large startup config blobs
    (GrowthBook / bootstrap) are collapsed to a top-level key list — their
    fingerprint value is the call + headers, not the server config payload."""
    if not text:
        return None
    try:
        obj = json.loads(text)
    except Exception:
        return {"_raw": text[:64]}
    if len(text) > 4000:
        if isinstance(obj, dict):
            return {"_keys": sorted(obj.keys()), "_size": len(text)}
        if isinstance(obj, list):
            return {"_array_len": len(obj), "_size": len(text)}
    return {"body": redact_oauth(obj)}


def summarize_body(text, url):
    """Return a redacted JSON object plus light structural notes."""
    try:
        obj = json.loads(text)
    except Exception:
        # base64/binary or plain text (e.g. releases endpoint = bare version)
        return {"_raw": text[:64]} if text else None
    notes = {}
    if isinstance(obj, dict):
        if isinstance(obj.get("messages"), list):
            notes["message_count"] = len(obj["messages"])
            notes["roles"] = collections.Counter(
                m.get("role") for m in obj["messages"] if isinstance(m, dict))
            notes["roles"] = dict(notes["roles"])
        if isinstance(obj.get("system"), list):
            notes["system_cache_pattern"] = [
                ("scope" in (b.get("cache_control") or {})) if isinstance(b, dict)
                and b.get("cache_control") else None
                for b in obj["system"]]
        if isinstance(obj.get("tools"), list):
            notes["tool_names"] = [t.get("name") for t in obj["tools"]
                                   if isinstance(t, dict)]
        redact_user_id(obj)
    if isinstance(obj, list) and obj and isinstance(obj[0], dict) and "events" not in obj[0]:
        notes["array_len"] = len(obj)
    red = redact(obj)
    return {"body": red, "_notes": notes} if notes else {"body": red}


def event_histogram(text):
    try:
        obj = json.loads(text)
    except Exception:
        return None
    evs = obj.get("events") if isinstance(obj, dict) else None
    if not isinstance(evs, list):
        return None
    return dict(collections.Counter(
        e.get("event_data", {}).get("event_name") for e in evs))


# Which sessions to keep: one representative per endpoint class. picker returns
# a sort key (class order, -bytes) so we grab the largest example of each class.
CLASSES = [
    # OAuth login + startup flow (chronological). Bodies summarized with
    # summarize_oauth (value-masked, structure kept).
    ("oauth_hello",   lambda u: "/v1/oauth/hello" in u),
    ("oauth_token",   lambda u: "/v1/oauth/token" in u),
    ("oauth_profile", lambda u: "/api/oauth/profile" in u),
    ("oauth_roles",   lambda u: "claude_cli/roles" in u),
    ("oauth_referral", lambda u: "referral/eligibility" in u),
    ("startup_eval_sdk", lambda u: "/api/eval/" in u),
    ("startup_grove",    lambda u: "claude_code_grove" in u),
    ("startup_bootstrap", lambda u: "claude_cli/bootstrap" in u),
    ("startup_penguin",   lambda u: "penguin_mode" in u),
    # Chat + telemetry (structurally redacted via summarize_body).
    ("v1_messages",   lambda u: "/v1/messages?beta" in u),
    ("count_tokens",  lambda u: "count_tokens" in u),
    ("event_logging_startup", lambda u: "event_logging" in u),  # largest = fat batch
    ("event_logging_steady",  lambda u: "event_logging" in u),  # smallest = single event
    ("datadog",       lambda u: "datadoghq" in u),
    ("releases",      lambda u: "claude-code-releases/latest" in u),
]

# Classes whose bodies go through summarize_oauth (verbatim-but-value-masked)
# instead of the structural redactor.
OAUTH_CLASSES = {"oauth_hello", "oauth_token", "oauth_profile", "oauth_roles",
                 "oauth_referral", "startup_eval_sdk", "startup_grove",
                 "startup_bootstrap", "startup_penguin"}


def main():
    if len(sys.argv) < 2:
        sys.exit("usage: extract_live.py <whistle-dump.json> [outdir]")
    src = json.load(open(sys.argv[1]))
    out_dir = sys.argv[2] if len(sys.argv) > 2 else os.path.join(CRACK_ROOT, "claude", "rows")
    os.makedirs(out_dir, exist_ok=True)
    sessions = src["data"]["data"]

    rows = []
    for k, v in sessions.items():
        url = v.get("url", "")
        rq, rs = v.get("req", {}), v.get("res", {})
        rqb = body_bytes(rq)
        rsb = body_bytes(rs)
        rows.append({
            "rowId": k, "startTime": v.get("startTime"), "url": url,
            "method": rq.get("method"), "status": rs.get("statusCode"),
            "reqHeaders": rq.get("headers", {}), "resHeaders": rs.get("headers", {}),
            "reqSize": rq.get("size"), "resSize": rs.get("size"),
            "_reqText": rqb.decode("utf-8", "replace") if rqb else None,
            "_resText": rsb.decode("utf-8", "replace") if rsb else None,
        })

    picked, manifest = {}, []
    for cls, pred in CLASSES:
        cands = [r for r in rows if pred(r["url"]) and r["_reqText"] is not None
                 or (pred(r["url"]) and cls == "releases")]
        cands = [r for r in rows if pred(r["url"])]
        if not cands:
            continue
        reverse = "steady" not in cls  # steady = smallest, else largest
        cand = sorted(cands, key=lambda r: r.get("reqSize") or 0, reverse=reverse)[0]
        if cand["rowId"] in picked and cls != "event_logging_steady":
            continue
        picked.setdefault(cand["rowId"], cls)

        def hdr(h): return {k: ("<masked>" if k.lower() in MASK_HEADERS else val)
                            for k, val in (h or {}).items()}
        rec = {
            "class": cls, "url": cand["url"], "method": cand["method"],
            "status": cand["status"], "reqSize": cand["reqSize"], "resSize": cand["resSize"],
            "reqHeaders": hdr(cand["reqHeaders"]), "resHeaders": hdr(cand["resHeaders"]),
        }
        if "event_logging" in cls:
            rec["event_histogram"] = event_histogram(cand["_reqText"])
        if cls in OAUTH_CLASSES:
            rec["reqBody"] = summarize_oauth(cand["_reqText"])
            rec["resBody"] = summarize_oauth(cand["_resText"])
        else:
            rec["reqBody"] = summarize_body(cand["_reqText"], cand["url"]) if cand["_reqText"] else None
            rec["resBody"] = summarize_body(cand["_resText"], cand["url"]) if cand["_resText"] else None
        # Universal identity scrub (defense-in-depth over UUID/email/hash).
        rec = scrub_identity(rec)
        idx = len(manifest) + 1
        fn = os.path.join(out_dir, f"{idx:02d}-{cls}.json")
        json.dump(rec, open(fn, "w"), indent=2, ensure_ascii=False)
        manifest.append({"idx": idx, "class": cls, "file": os.path.basename(fn),
                         "url": _scrub_str(cand["url"]), "status": cand["status"],
                         "reqSize": cand["reqSize"]})
        print(f"{idx:02d} {cls:24s} {cand['status']} req={cand['reqSize']}  {cand['url'][:60]}")
    json.dump(manifest, open(os.path.join(out_dir, "_manifest.json"), "w"),
              indent=2, ensure_ascii=False)


if __name__ == "__main__":
    main()
