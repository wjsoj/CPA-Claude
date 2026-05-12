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
