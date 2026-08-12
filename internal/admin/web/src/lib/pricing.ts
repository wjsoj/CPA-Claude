import type { Pricing, PricingEntry } from "./types";

// canonicalProvider mirrors pricing.canonicalProvider in
// internal/pricing/pricing.go — empty/"anthropic"/"claude" → anthropic;
// "openai"/"codex"/"chatgpt" → openai; everything else passes through.
export function canonicalProvider(p: string | undefined | null): string {
  const v = (p || "").toLowerCase().trim();
  if (v === "" || v === "anthropic" || v === "claude") return "anthropic";
  if (v === "openai" || v === "codex" || v === "chatgpt") return "openai";
  return v;
}

// lookupPrice mirrors pricing.Catalog.Lookup in internal/pricing/pricing.go.
// Resolution order: exact "<provider>/<model>" → hyphen-prefix walk under
// the same provider → provider_defaults[provider] → pricing.default.
// Always pair the model with its provider — the catalog keys are stored as
// "anthropic/claude-opus-4-7", so a bare-model lookup misses and silently
// falls back to the sonnet-priced default, which under-reports the cost.
export function lookupPrice(
  pricing: Pricing | null | undefined,
  provider: string | undefined | null,
  model: string | undefined | null,
): PricingEntry | null {
  if (!pricing) return null;
  const models = pricing.models || {};
  const prov = canonicalProvider(provider);
  let m = (model || "").toLowerCase().trim();
  if (m.endsWith(")")) {
    const i = m.lastIndexOf("(");
    if (i > 0) m = m.slice(0, i).trim();
  }
  if (m) {
    const full = `${prov}/${m}`;
    if (models[full]) return models[full];
    for (let i = m.lastIndexOf("-"); i > 0; i = m.lastIndexOf("-", i - 1)) {
      const p = models[`${prov}/${m.slice(0, i)}`];
      if (p) return p;
    }
  }
  const provDef = pricing.provider_defaults?.[prov];
  if (provDef) return provDef;
  return pricing.default || null;
}

// lookupPriceAnyProvider resolves a bare model name (no provider prefix) by
// scanning every catalog entry and matching against the suffix after the
// "/". Use when the data source (e.g. requestlog.ByModel) keys aggregates
// by bare model and the caller has no provider context. Returns the
// pricing.default (NOT provider_defaults — those need a provider) when no
// model-level match exists.
export function lookupPriceAnyProvider(
  pricing: Pricing | null | undefined,
  model: string | undefined | null,
): PricingEntry | null {
  if (!pricing) return null;
  const models = pricing.models || {};
  let m = (model || "").toLowerCase().trim();
  if (m.endsWith(")")) {
    const i = m.lastIndexOf("(");
    if (i > 0) m = m.slice(0, i).trim();
  }
  if (m) {
    // Exact match on bare suffix.
    for (const [key, val] of Object.entries(models)) {
      const slash = key.indexOf("/");
      const suffix = slash >= 0 ? key.slice(slash + 1) : key;
      if (suffix === m) return val;
    }
    // Prefix walk on bare suffix.
    for (let i = m.lastIndexOf("-"); i > 0; i = m.lastIndexOf("-", i - 1)) {
      const trimmed = m.slice(0, i);
      for (const [key, val] of Object.entries(models)) {
        const slash = key.indexOf("/");
        const suffix = slash >= 0 ? key.slice(slash + 1) : key;
        if (suffix === trimmed) return val;
      }
    }
  }
  return pricing.default || null;
}

// ByModelTokens is the subset of requestlog.Aggregate the cache math needs,
// keyed by bare model name.
export interface ByModelTokens {
  [model: string]: {
    input_tokens?: number;
    output_tokens?: number;
    cache_read_tokens?: number;
    cache_create_tokens?: number;
  };
}

export interface CacheSavings {
  hitRate: number;
  /** What the measured tokens cost under the catalog, caching included. */
  cachedCost: number;
  /** What those same tokens would have cost with every read billed as input. */
  noCacheCost: number;
  savings: number;
  input: number;
  output: number;
  cacheRead: number;
  cacheCreate: number;
  totalTokens: number;
}

// cacheSavings prices one set of per-model token aggregates twice — once as
// billed, once as if no prompt cache existed — and returns the delta.
//
// BOTH sides are derived from the same tokens through the same catalog. It is
// tempting to use the recorded cost_usd as the "with cache" side since it is
// the authoritative number, but the two are not the same basis and subtracting
// them is meaningless:
//
//   - Rows can carry a cost with no token counts at all. Production's archive
//     predating 2026-08-09 was reconstructed from the wallet ledger, which
//     records amounts and not tokens: 802k of 946k rows, $86k of cost, zero
//     tokens. Those dollars landed on the recorded side and nothing landed on
//     the modelled side, so "no-cache $73k − actual $121k" went negative and
//     the card clamped to $0.00.
//   - Even with intact rows the recorded cost reflects things the catalog
//     re-derivation cannot see: billingModelFor rewrites (opus-4-7 priced as
//     opus-5), per-row config price overrides, and dated introductory prices.
//
// So the card answers "what did caching save on the traffic we have token
// data for", which is the question it was always meant to answer, and rows
// with no token data simply do not participate on either side.
export function cacheSavings(
  pricing: Pricing | null | undefined,
  byModel: ByModelTokens | null | undefined,
): CacheSavings | null {
  if (!pricing || !byModel) return null;
  let cachedCost = 0;
  let noCacheCost = 0;
  let input = 0;
  let output = 0;
  let cacheRead = 0;
  let cacheCreate = 0;
  for (const [name, a] of Object.entries(byModel)) {
    const ain = a.input_tokens || 0;
    const acr = a.cache_read_tokens || 0;
    const acw = a.cache_create_tokens || 0;
    const aout = a.output_tokens || 0;
    input += ain;
    output += aout;
    cacheRead += acr;
    cacheCreate += acw;
    const p = lookupPriceAnyProvider(pricing, name);
    if (!p) continue;
    // Cards that price cache writes at 0 (OpenAI never reports them) bill
    // those tokens at the plain input rate, matching pricing.ModelPrice.Cost.
    const cwRate = p.cache_create_per_1m > 0 ? p.cache_create_per_1m : p.input_per_1m;
    cachedCost +=
      (ain * p.input_per_1m + aout * p.output_per_1m + acr * p.cache_read_per_1m + acw * cwRate) /
      1e6;
    noCacheCost += ((ain + acr + acw) * p.input_per_1m + aout * p.output_per_1m) / 1e6;
  }
  const denom = input + cacheRead + cacheCreate;
  return {
    hitRate: denom > 0 ? cacheRead / denom : 0,
    cachedCost,
    noCacheCost,
    // Cache writes cost more than plain input (anthropic: $3.75 vs $3), so a
    // window that wrote far more than it read genuinely saved nothing. Clamp
    // rather than show a negative "saving".
    savings: Math.max(0, noCacheCost - cachedCost),
    input,
    output,
    cacheRead,
    cacheCreate,
    totalTokens: input + output + cacheRead + cacheCreate,
  };
}

// matchedModelKey returns the canonical catalog key that lookupPrice would
// match, or null if the lookup falls back to a provider/global default.
// Useful for UIs that want to show the user which catalog entry was used
// (e.g. "matched anthropic/claude-opus-4-7 (prefix)").
export function matchedModelKey(
  pricing: Pricing | null | undefined,
  provider: string | undefined | null,
  model: string | undefined | null,
): string | null {
  if (!pricing) return null;
  const models = pricing.models || {};
  const prov = canonicalProvider(provider);
  let m = (model || "").toLowerCase().trim();
  if (m.endsWith(")")) {
    const i = m.lastIndexOf("(");
    if (i > 0) m = m.slice(0, i).trim();
  }
  if (!m) return null;
  const full = `${prov}/${m}`;
  if (models[full]) return full;
  for (let i = m.lastIndexOf("-"); i > 0; i = m.lastIndexOf("-", i - 1)) {
    const key = `${prov}/${m.slice(0, i)}`;
    if (models[key]) return key;
  }
  return null;
}
