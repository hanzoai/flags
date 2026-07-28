# hanzoai/flags

**Feature flags, percentage rollouts, and A/B experiments for the Hanzo platform — one engine, a client in every language.**

The engine is native to [Hanzo Cloud](https://github.com/hanzoai/cloud): flag
definitions live in per-org/project SQLite (encrypted at rest), and evaluation
runs in-process with **PostHog-compatible semantics** — identical rollout hash,
property operators, variants, and payload shapes. No external flags service, no
KV, no network hop on the hot path.

This repo is the canonical home of the **evaluator** and the **client SDKs**.

The evaluator itself is [`go/`](go/) — pure Go, zero dependencies, importable by
anyone who wants to decide flags locally instead of over HTTP. It replaced a
Rust staticlib that cloud linked over cgo; `go/testdata/oracle.json` holds that
implementation's answer to all 621 cases in the parity table, and
`go/parity_test.go` is the standing proof the two still agree — identity by
identity on the rollout hash, and character for character on every rendered
value.

Every client speaks the same two calls:

| Call | What |
| --- | --- |
| `POST /v1/flags` | Evaluate all flags for one identity → `{featureFlags, featureFlagPayloads}` |
| `PUT/GET/DELETE /v1/flags/defs[/:key]` | Manage definitions (audited, versioned) |

Auth: `Authorization: Bearer <HANZO_API_KEY>` server-side; browsers ride the
gateway session. Scope to a project with `X-Project-Id` (omit for the org
default). Full docs: **https://docs.hanzo.ai/docs/flags**

---

## TypeScript / JavaScript — [`@hanzo/flags`](https://www.npmjs.com/package/@hanzo/flags)

```bash
npm install @hanzo/flags
```

```ts
import { HanzoFlags } from "@hanzo/flags"

const flags = new HanzoFlags({
  host: "https://api.hanzo.ai",
  token: process.env.HANZO_API_KEY, // omit in-browser
  project: "web",                    // optional
})

await flags.load({ distinctId: user.id, personProperties: { plan: "pro" } })

flags.isEnabled("new-nav")          // boolean
flags.getVariant("checkout-exp")    // "control" | "treatment" | null
flags.getPayload("checkout-exp")    // { cta: "Buy now" }
```

Results cache for a 15s TTL per identity and degrade fail-safe (network error →
previous values). One-shot server-side: `evaluateFlags(config, ctx)`.
Source: [`typescript/`](typescript/).

## Go — `github.com/hanzoai/flags/go`

```bash
go get github.com/hanzoai/flags/go
```

```go
import flags "github.com/hanzoai/flags/go"

c := flags.New("https://api.hanzo.ai", flags.WithToken(os.Getenv("HANZO_API_KEY")))

res, err := c.Evaluate(ctx, flags.Context{
    DistinctID:       "user-42",
    PersonProperties: map[string]any{"plan": "pro"},
})

res.IsEnabled("new-nav")          // bool
res.Variant("checkout-exp")       // "treatment" | ""
var cfg struct{ CTA string `json:"cta"` }
res.Payload("checkout-exp", &cfg) // decode the payload
```

The same package evaluates locally, with no server involved — this is the engine
cloud runs:

```go
res := flags.Evaluate(defs, flags.Context{
    DistinctID:       "user-42",
    PersonProperties: map[string]any{"plan": "pro"},
})
```

`Evaluate` is pure: same definitions and same context, same answer, on every
host and in every process. `EvaluateJSON(defs, ctx []byte) ([]byte, error)` is
the wire-shaped equivalent.

Inside **hanzoai/cloud** itself, don't use the HTTP client — call the in-process
seam directly: `flags.Bool/Int/String(key)` (zero-copy, no HTTP).
Source: [`go/`](go/).

## Python — `hanzo-flags`

```bash
pip install hanzo-flags   # or: uv add hanzo-flags
```

```python
from hanzo_flags import Flags

flags = Flags("https://api.hanzo.ai", token=os.environ["HANZO_API_KEY"])

res = flags.evaluate("user-42", person_properties={"plan": "pro"})

res.is_enabled("new-nav")         # bool
res.variant("checkout-exp")       # "treatment" | None
res.payload("checkout-exp")       # {"cta": "Buy now"}
```

Zero dependencies (stdlib urllib). Source: [`python/`](python/).

## Rust — `hanzo-flags-client`

```toml
[dependencies]
hanzo-flags-client = "0.1"
```

```rust
use hanzo_flags_client::{Flags, Context};

let flags = Flags::new("https://api.hanzo.ai")
    .with_token(std::env::var("HANZO_API_KEY")?);

let res = flags.evaluate(Context::new("user-42").prop("plan", "pro")).await?;

res.is_enabled("new-nav");          // bool
res.variant("checkout-exp");        // Option<&str>
res.payload("checkout-exp");        // Option<&serde_json::Value>
```

For **in-process** evaluation (no HTTP at all — embed the evaluator itself),
depend on the `hanzo-flags` evaluator crate in
[`cloud/native/flags`](https://github.com/hanzoai/cloud/tree/main/native/flags):
`evaluate(&[FlagDef], &EvalContext) -> EvalResponse`, or over C FFI as
`hanzo_flags_evaluate(defs_json, ctx_json)`. Source: [`rust/`](rust/).

## C / C++ — FFI

The evaluator ships a C ABI (link `libhanzo_flags.a` from
[`cloud/native/flags`](https://github.com/hanzoai/cloud/tree/main/native/flags)):

```c
extern char* hanzo_flags_evaluate(const char* defs_json, const char* ctx_json);
extern void  hanzo_flags_free(char* result);

char* out = hanzo_flags_evaluate(defs, "{\"distinct_id\":\"user-42\"}");
/* out = {"featureFlags":{...},"featureFlagPayloads":{...}} — parse, then: */
hanzo_flags_free(out);
```

Pure function, panic-guarded, errors returned as `{"error": "..."}`.

---

## Curl (any language)

```bash
# Evaluate
curl -X POST https://api.hanzo.ai/v1/flags \
  -H "Authorization: Bearer $HANZO_API_KEY" -H "Content-Type: application/json" \
  -d '{"distinct_id":"user-42","person_properties":{"plan":"pro"}}'

# Define / roll out / A-B
curl -X PUT https://api.hanzo.ai/v1/flags/defs/checkout-exp \
  -H "Authorization: Bearer $HANZO_API_KEY" -H "Content-Type: application/json" \
  -d '{"active":true,"filters":{
        "groups":[{"properties":[],"rollout_percentage":50}],
        "multivariate":{"variants":[
          {"key":"control","rollout_percentage":50},
          {"key":"treatment","rollout_percentage":50}]},
        "payloads":{"treatment":{"cta":"Buy now"}}}}'
```

## Platform switches (operators)

Waitlist intake, public signup, invite gating, gateway limits — the same engine
evaluated from a reserved platform scope, managed at **admin.hanzo.ai**:
`GET /v1/admin/flags` (switchboard) · `PUT /v1/admin/flags/:key` (flip;
SuperAdmin). Flips apply hot within one TTL — no redeploy.

## License

MIT
