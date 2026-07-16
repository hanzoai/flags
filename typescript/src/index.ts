/**
 * @hanzo/flags — feature flags + A/B for the Hanzo native flags engine.
 *
 * Talks to cloud's /v1/flags (the stateless Rust evaluator embedded in
 * hanzoai/cloud; definitions in per-org/project SQLite). Responses are
 * PostHog-shaped ({featureFlags, featureFlagPayloads}), so this client also
 * works against any compatible evaluator.
 *
 * Usage:
 *   import { HanzoFlags } from "@hanzo/flags"
 *
 *   const flags = new HanzoFlags({
 *     host: "https://api.hanzo.ai",     // any surface serving /v1/flags
 *     token: process.env.HANZO_API_KEY,  // bearer; omit in-browser (gateway session)
 *     project: "web",                    // optional X-Project-Id scope
 *   })
 *
 *   await flags.load({ distinctId: user.id, personProperties: { plan: "pro" } })
 *   if (flags.isEnabled("checkout-exp")) { ... }
 *   switch (flags.getVariant("checkout-exp")) { case "treatment": ... }
 *   const cfg = flags.getPayload("checkout-exp")
 */

export interface FlagsConfig {
  /** Base URL serving /v1/flags (e.g. https://api.hanzo.ai). */
  host: string
  /** Bearer token for server-side use; browsers ride the gateway session. */
  token?: string
  /** Project scope (X-Project-Id). Default: the org's default project. */
  project?: string
  /** Cache TTL for load() results, ms. Default 15000. */
  ttlMs?: number
  /** Custom fetch (tests, polyfills). Default: globalThis.fetch. */
  fetch?: typeof fetch
}

export interface EvalContext {
  distinctId: string
  personProperties?: Record<string, unknown>
  /** group_type_index -> { key, properties } */
  groups?: Record<string, { key: string; properties?: Record<string, unknown> }>
}

export interface EvalResult {
  /** key -> true | false | "variant" */
  featureFlags: Record<string, boolean | string>
  /** key -> arbitrary JSON payload */
  featureFlagPayloads: Record<string, unknown>
  errorsWhileComputingFlags: boolean
}

const EMPTY: EvalResult = { featureFlags: {}, featureFlagPayloads: {}, errorsWhileComputingFlags: false }

export class HanzoFlags {
  private cfg: FlagsConfig
  private result: EvalResult = EMPTY
  private loadedAt = 0
  private ctxKey = ""

  constructor(cfg: FlagsConfig) {
    if (!cfg?.host) throw new Error("@hanzo/flags: host is required")
    this.cfg = { ttlMs: 15_000, ...cfg }
  }

  /**
   * Evaluate the caller's flags for one identity. Results are cached for the
   * TTL (per identity) — repeated reads are free; a changed context reloads.
   * Fail-safe: on a network/engine error the previous result is kept.
   */
  async load(ctx: EvalContext): Promise<EvalResult> {
    const key = JSON.stringify(ctx)
    const fresh = this.ctxKey === key && Date.now() - this.loadedAt < (this.cfg.ttlMs ?? 15_000)
    if (fresh) return this.result

    const f = this.cfg.fetch ?? globalThis.fetch
    const headers: Record<string, string> = { "Content-Type": "application/json" }
    if (this.cfg.token) headers.Authorization = `Bearer ${this.cfg.token}`
    if (this.cfg.project) headers["X-Project-Id"] = this.cfg.project
    try {
      const res = await f(`${this.cfg.host.replace(/\/$/, "")}/v1/flags`, {
        method: "POST",
        headers,
        credentials: this.cfg.token ? undefined : "include",
        body: JSON.stringify({
          distinct_id: ctx.distinctId,
          person_properties: ctx.personProperties,
          groups: ctx.groups,
        }),
      })
      if (!res.ok) throw new Error(`flags: ${res.status}`)
      const body = (await res.json()) as EvalResult
      this.result = {
        featureFlags: body.featureFlags ?? {},
        featureFlagPayloads: body.featureFlagPayloads ?? {},
        errorsWhileComputingFlags: !!body.errorsWhileComputingFlags,
      }
      this.ctxKey = key
      this.loadedAt = Date.now()
    } catch {
      // fail-safe: keep the previous result; retry after TTL
      this.loadedAt = Date.now()
      this.ctxKey = key
    }
    return this.result
  }

  /** True when the flag is on (boolean true or any variant). */
  isEnabled(key: string): boolean {
    const v = this.result.featureFlags[key]
    return v === true || (typeof v === "string" && v !== "")
  }

  /** The variant key for multivariate flags, or null. */
  getVariant(key: string): string | null {
    const v = this.result.featureFlags[key]
    return typeof v === "string" ? v : null
  }

  /** The flag's payload (per-variant or the "true" payload), or null. */
  getPayload<T = unknown>(key: string): T | null {
    return (this.result.featureFlagPayloads[key] as T) ?? null
  }

  /** The raw last evaluation result. */
  all(): EvalResult {
    return this.result
  }
}

/**
 * One-shot server-side evaluation — no instance, no cache.
 */
export async function evaluateFlags(cfg: FlagsConfig, ctx: EvalContext): Promise<EvalResult> {
  const c = new HanzoFlags({ ...cfg, ttlMs: 0 })
  return c.load(ctx)
}
