[thinking] # PM Handoff — Body-less POSTs panic on Lambda (nil `req.Body`, fleet-wide)

## 0. Context digest (what I verified before scoping)

- **Root cause, confirmed in code:** the SDK never builds `*http.Request` itself for an AWS event — that's done by two third-party libs it depends on: `awslabs/aws-lambda-go-api-proxy` (`core.EventToRequestWithContext`, used by the gin/api-gateway and gin/function-url paths) and `its-felix/aws-lambda-go-http-adapter` (`convertFunctionURLRequest` / `convertApiGwV1Request`, used by the echo/streaming and vanilla paths). **Only the its-felix path produces a nil body**: its `getBody(body, isB64)` in `handler/common.go` returns a bare `nil` `io.Reader` when `body == ""`, and `http.NewRequestWithContext` leaves `req.Body == nil` when given a nil reader. The `aws-lambda-go-api-proxy` path (`core/request.go`) always passes `bytes.NewReader(decodedBody)` — never nil — so the gin/api-gateway and gin/function-url routing types are **not** affected; only the echo/streaming router and the `WithVanillaHandler` escape hatch are.
- **Single SDK choke point that doesn't touch call sites:** `pkg/service/http_adapter.go`'s `GinAdapter(...)` and `EchoAdapter(...)` wrapper functions already run on every single request for both frameworks, before any handler or the SDK's own middleware chain executes. Normalising `req.Body == nil → http.NoBody` there (two one-line guards) closes the gap once, fleet-wide, with no call-site changes anywhere — exactly the "one normalisation, not one middleware per service" the brief asks for. `pkg/service/helpers.go`'s `ReadBody`/`ReadBytes` already nil-guard defensively (merged 2026-09-30, commit `2a8b117c`), but that's a symptom guard inside the SDK's own helper — it does **not** help the fleet's dozens of call sites that do `json.NewDecoder(c.Request().Body).Decode(...)` directly (confirmed live in `forge-aigateway`: `embedding.go:191`, `admin_handlers.go` cache-invalidation handler, `allowance_handler.go:47` — all bypass `ReadBody` entirely).
- **`WithVanillaHandler` is a known gap, explicitly out of scope for this slice:** that escape hatch hands the raw its-felix-built `*http.Request` straight to a caller-owned `http.Handler`, bypassing `GinAdapter`/`EchoAdapter` entirely. No fleet service currently uses it (unconfirmed by grep — Architect should re-check at implementation time), so fixing it is NICE-TO-HAVE, not MUST.
- **Blast radius (from the roadmap item, already surveyed 2026-09-29):** `forge-conductor` has a local `normalizeBodyMiddleware` already shipped (commit `cd94b792`, live-verified) covering ~122 sites — **do not touch, do not remove this run**, per explicit brief instruction. `forge-aigateway` has exactly 3 exposed unguarded decode sites (listed above) and depends on `go-aws-lambda-sdk v0.0.0-20260930194956-2a8b117cc526` today — the obvious "at least one dependent service" candidate for the build+uptake check, since I already have read access and confirmed its three panic sites.
- **net/http invariant this restores:** `http.NoBody` is what `net/http`'s own server hands a handler for an empty body — never `nil`. The fix makes the Lambda adapter honour that same invariant at construction time, which is what every unguarded `json.NewDecoder(...).Decode()` in the fleet already (implicitly) assumes.

## 1. Requirements (restated from brief)

1. Normalise empty-body Lambda requests to `http.NoBody` at SDK request construction — **one fix in the SDK**, not per-service middleware.
2. Add focused tests: body-less POST (no panic, decodes/EOF behaves like `http.NoBody`) and non-empty POST (body preserved byte-for-byte).
3. Prove fleet blast radius is bounded: confirm at least one dependent service (not just the SDK itself) builds against the fixed SDK.
4. Live smoke: a body-less POST to an optional-body endpoint on a **non-conductor** service returns its normal response (not a panic/500).
5. Do **not** remove `forge-conductor`'s `normalizeBodyMiddleware` this run — that's gated on all six consumers deploying the new SDK version, which is explicitly out of scope here.
6. Do not claim fleet-wide live DoD — only the one smoked service.

## 2. Scope decisions

| # | Item | MUST / NICE / CUT | Rationale |
|---|---|---|---|
| 1 | Normalise `req.Body == nil → http.NoBody` inside `GinAdapter` and `EchoAdapter` wrapper functions in `pkg/service/http_adapter.go` | **MUST** | Single choke point, zero call-site churn, covers both routing types the fleet actually uses |
| 2 | Unit test: body-less POST through the adapter path never panics and downstream `Body.Read` returns `io.EOF` immediately (the `http.NoBody` contract) | **MUST** | Brief's explicit AC |
| 3 | Unit test: non-empty POST body is preserved unchanged end-to-end through the adapter | **MUST** | Brief's explicit AC — regression guard against breaking the common case while fixing the edge case |
| 4 | Bump `forge-aigateway`'s `go.mod` to the new SDK commit/tag and run `go build ./...` there to prove a real consumer compiles | **MUST** | Brief's "confirm at least one dependent service builds" — `forge-aigateway` is the best candidate: already has 3 confirmed exposed panic sites, so the build check doubles as proof the fix is reachable |
| 5 | Live smoke: body-less POST against one of `forge-aigateway`'s 3 exposed sites (`/api/internal/allowance-snapshot`, `/api/admin/usage/cache-invalidation` route, or `/v1/embeddings`) returns its normal 4xx/204 — not a panic | **MUST** | Brief's explicit live DoD, and must target a **non-conductor** service per brief |
| 6 | Normalise the same nil-body gap inside `WithVanillaHandler`'s its-felix adapter wiring | **NICE-TO-HAVE** | No confirmed fleet consumer of `WithVanillaHandler` today; fix opportunistically if cheap, don't block the slice on it |
| 7 | Remove `forge-conductor`'s `normalizeBodyMiddleware` | **CUT (explicitly, this run)** | Brief is explicit: only after all six consumers adopt the new SDK version. Removing it now would reopen the exact panic for conductor the moment someone reverts the SDK pin |
| 8 | Roll the SDK fix out to the other 4 dependent services (`forge-sessions`, `forge-runtime`, `forge-notifier`, `forge-storage`) in this run | **CUT** | Brief only asks to confirm *one* dependent service builds + smoke *one* non-conductor service; bumping all 4 is follow-up work, not this slice's DoD |
| 9 | A fleet-wide "safe version uptake" rollout plan (bump all consumers, stagger deploys) | **NICE-TO-HAVE, as a written plan only** | Brief says "plan safe version uptake" — a short paragraph/table in the Architect or DevOps handoff is enough; do not action the rollout itself this run |

## 3. Acceptance criteria (numbered, for downstream verification)

| AC# | Criterion | Owner |
|---|---|---|
| AC1 | `pkg/service/http_adapter.go`'s `GinAdapter` and `EchoAdapter` normalise a nil `*http.Request.Body` to `http.NoBody` before the wrapped handler/middleware chain runs | Developer |
| AC2 | New unit test proves a body-less POST routed through the adapter does not panic and the handler observes `http.NoBody`-equivalent behavior (immediate `io.EOF`, zero bytes) | Developer/QA |
| AC3 | New unit test proves a non-empty POST body passes through byte-for-byte unchanged (no truncation, no double-read) | Developer/QA |
| AC4 | `go test ./...` green in `go-aws-lambda-sdk` | Developer |
| AC5 | `forge-aigateway`'s `go.mod`/`go.sum` bumped to the new SDK commit; `go build ./...` succeeds there | Developer/DevOps |
| AC6 | Live smoke: a body-less POST to a `forge-aigateway` optional-body endpoint (e.g. `/api/internal/allowance-snapshot` with correct service-token auth, or another of the 3 listed sites) returns its normal response code — not a panic/5xx-with-stacktrace | QA/DevOps (needs a reachable aigateway deployment + any required auth token) |
| AC7 | `forge-conductor`'s `normalizeBodyMiddleware` is left untouched in this run | Developer (verify via diff — no edits to `internal/service/server.go` normalizeBodyMiddleware or `internal/service/handlers/httpbody/body.go` in forge-conductor) |
| AC8 | Handoff documents a safe-uptake plan (which of the 4 remaining consumers still need the bump, suggested order) without actioning it | Architect/PM (this doc, §5 below) |

## 4. Out-of-scope (explicit, do not let scope creep in)

- Removing `forge-conductor`'s middleware (gated, future slice).
- Bumping `forge-sessions` / `forge-runtime` / `forge-notifier` / `forge-storage` to the new SDK (0 unguarded decode sites surveyed 2026-09-29 — low urgency; still needed eventually for the "SDK is the durable fix" to be true everywhere, but not this slice's DoD).
- Fixing `WithVanillaHandler`'s its-felix wiring (no confirmed consumer; NICE-TO-HAVE only if trivial).
- Any change to `aws-lambda-go-api-proxy` or `its-felix/aws-lambda-go-http-adapter` themselves — those are third-party deps; the SDK normalises **after** they hand back the request, not by patching them upstream.
- Any claim of "fleet-wide" live verification — only one non-conductor service gets smoked this run per the brief.

## 5. Safe version-uptake plan (AC8 — written plan only, not actioned)

| Service | Unguarded decode sites (2026-09-29 survey) | Action this run | Follow-up |
|---|---|---|---|
| forge-conductor | 122 (covered by local middleware `cd94b792`) | none — keep middleware | Bump SDK + delete middleware in a LATER slice once all 6 are confirmed on the new version |
| forge-aigateway | 3 (`embedding.go:191`, admin cache-invalidation handler, `allowance_handler.go:47`) | **bump + smoke this run (AC5/AC6)** | — |
| forge-sessions / forge-runtime / forge-notifier / forge-storage | 0 each | none | Bump opportunistically on next unrelated SDK-consuming change to each repo; no urgency since no call site is currently reachable, but they inherit the fix "for free" the next time their go.mod is touched |

Suggested rollout order: aigateway (done this run) → conductor (last, because deleting its middleware is the gated step) → the zero-site services whenever convenient.

## 6. Handoff guidance for Architect

- **Insertion point is settled** — don't re-litigate where the fix goes; see §0. The normalisation belongs in `GinAdapter`/`EchoAdapter` in `pkg/service/http_adapter.go`, applied to `*http.Request.Body` before invoking the wrapped `HttpAdapterHandler`. Two `if r.Body == nil { r.Body = http.NoBody }` guards, one per adapter function.
- **Test file to extend:** `pkg/service/helpers_test.go` already has the exact pattern needed (`newBodyAdapter`, `malformedBodies`, nil-body/nil-service test shapes) — follow that style for the new adapter-level tests rather than inventing a new harness. Consider a new `pkg/service/http_adapter_test.go` since these tests exercise `GinAdapter`/`EchoAdapter` directly (constructing a `*gin.Context`/`echo.Context` with a nil-body `httptest.NewRequest`), not `ReadBody`.
- **Dependent-service check:** `forge-aigateway`'s `go.mod` pins `github.com/simple-container-com/go-aws-lambda-sdk v0.0.0-20260930194956-2a8b117cc526` — bump the pseudo-version to the new commit after merge, run `go build ./...` there (not full test suite — scope to build only, per the brief's "confirm ... builds").
- **Live smoke target:** `POST /api/internal/allowance-snapshot` on forge-aigateway needs a valid service-token (`validateServiceToken(c, "")` — any mesh service JWT, no admin scope). If that auth is awkward to obtain live, the embeddings endpoint (`POST /v1/embeddings`) or admin usage endpoints may be an easier body-less-POST target — Architect/QA should pick whichever has the lowest-friction auth path reachable from this run's tooling. **This is the one place I expect an operator-credential blocker** (a service token or API key QA/DevOps may not hold) — if so, stop there, name the exact secret alias needed, and mark AC6 FAIL-blocked rather than hand-waving it.
- **Versioning:** this is a backward-compatible bugfix (nil→NoBody is strictly safer than nil), so a patch/minor SDK version bump is appropriate — no breaking-change notice needed for consumers.

**Empty-PR-OK:** no — this is a planning turn ahead of the Architect/Developer's code change; no source diff is expected from me.

**Verdict:** signoff