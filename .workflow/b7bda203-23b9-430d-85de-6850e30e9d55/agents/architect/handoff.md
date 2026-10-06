[thinking] # Architect Handoff — Body-less POSTs panic on Lambda (nil `req.Body`, fleet-wide)

## 0. Role scope this turn

This is a design/handoff turn only. I read the real `go-aws-lambda-sdk` source (not from training-data assumption) to confirm PM's proposed insertion point is sound, pinned exact code to touch, and found one real gap PM's plan didn't close: **which routing mode `forge-aigateway` actually runs is unconfirmed** — that changes what AC6's live smoke can prove. I did not write code this turn; that is the Developer's job next.

## 1. Commit + branch identity

No code mutations in this turn. Repo `go-aws-lambda-sdk` was fully accessible (confirmed via `list_repository_tree`/`read_repository_file` against `simple-container-com/go-aws-lambda-sdk`, default branch `main`) — this is a design-only handoff, not a "repo inaccessible" case.

## 2. Files touched

| Path | Lines added / removed | What changed |
|---|---|---|
| — | — | none (design turn) |

## 3. Design verification against real source (what I confirmed, with evidence)

| # | Claim | Verified how | Evidence |
|---|---|---|---|
| D1 | `GinAdapter`/`EchoAdapter` in `pkg/service/http_adapter.go` are the single point every registered route handler passes through | Read `pkg/service/http_adapter.go` + `pkg/service/router.go` in full | `router.go`: `ginRouter.GET/POST/DELETE/PATCH/PUT/OPTIONS/HEAD` all call `GinAdapter(h, g.logger, g.localDebug)`; `echoRouter`/`echoGroup` equivalents all call `EchoAdapter(h, e.logger, e.localDebug)`. Confirmed no other construction path for a route handler. |
| D2 | Exact closures to patch | Read `http_adapter.go` verbatim | `GinAdapter(...) func(*gin.Context)` returns `func(g *gin.Context) { if err := callback(&ginAdapter{c: g, ...}); err != nil {...} }` — guard goes immediately before the `callback(...)` call, using `g.Request` (the `*http.Request`, a pointer, so mutating `.Body` in place needs no `SetRequest`-equivalent). `EchoAdapter(...) func(c echo.Context) error` returns `func(c echo.Context) error { return callback(&echoAdapter{c: c, ...}) }` — guard goes on `c.Request()` (also `*http.Request`) before the `callback(...)` call. |
| D3 | Root cause is confined to the its-felix path, not aws-lambda-go-api-proxy | Read `pkg/service/service.go`'s `New()` | Gin path (`routingType == function-url` or `api-gateway`) is wired via `ginadapter.New(ginRouter)` (`awslabs/aws-lambda-go-api-proxy`); Echo/streaming path is wired via `echohandler.NewFunctionURLStreamingHandler(echoadapter.NewEchoAdapter(echoRouter))` (`its-felix/aws-lambda-go-http-adapter`) — consistent with PM's finding that only the its-felix adapter's `getBody` can hand back a bare `nil` reader. Patching **both** `GinAdapter` and `EchoAdapter` is still correct: it's defense-in-depth for the Gin path (cheap, harmless) and the actual fix for the Echo path. |
| D4 | Existing nil-guards in the SDK (`ReadBody`/`ReadBytes`) don't close this gap | Read `pkg/service/helpers.go` | `ReadBody` treats `c.RequestBody() == nil` as a 400 (safe), but any call site that does `json.NewDecoder(c.Request().Body).Decode(...)` directly — bypassing `ReadBody` — still gets a raw possibly-nil `Body` and panics on `.Decode()`. Confirmed live example below (D5). |
| D5 | `forge-aigateway` has at least one unguarded decode site reachable through `HttpAdapter` | Read `internal/aigateway/allowance_handler.go` in `forge-aigateway` | `handleAllowanceSnapshot` (route `POST /api/internal/allowance-snapshot`): `if err := json.NewDecoder(c.Request().Body).Decode(&snap); err != nil { ... }` — `c.Request().Body` is read directly, no `ReadBody` helper, no nil guard. This is a real, present-day panic site if `c.Request().Body` is ever nil on that route. |
| D6 | `forge-aigateway` depends on the SDK version PM named | Read `go.mod` in `forge-aigateway` | `github.com/simple-container-com/go-aws-lambda-sdk v0.0.0-20260930194956-2a8b117cc526` — confirmed exact pseudo-version string. |

### ⚠ D7 — Open gap PM's plan did not close (structural concern, flagging before Developer starts)

I could **not** confirm, from the code I read, which routing mode `forge-aigateway` actually runs (`service.New(...)` with `UseResponseStreaming(true)` → Echo/its-felix path — the vulnerable one — vs. default Gin → `aws-lambda-go-api-proxy` path — the one PM's own root-cause analysis says **never** produces a nil body). I found the `Service` struct and `New()` constructor in `internal/aigateway/service.go` but the call that actually invokes the SDK's `service.New(...)` with its options (where `UseResponseStreaming` would be set) was not visible in what I read before hitting the turn's tool-call budget — repeated searches (`service.New(`, `WithRoutes`, `UseResponseStreaming(true)`) returned 0 hits, which I read as a code-search indexing gap, not proof it's absent.

**This matters**: if `forge-aigateway` runs the Gin/API-Gateway path exclusively, `allowance_handler.go`'s unguarded decode is reachable only if the Gin path can ALSO see a nil body somehow (e.g., via `WithVanillaHandler`, or if the deployed routing type is Function URL in RESPONSE_STREAM mode without the caller realizing it uses Echo). Developer must `grep -rn "service.New(\|UseResponseStreaming" internal/ cmd/` in a real clone of `forge-aigateway` before picking the AC6 smoke target — don't assume PM's candidate reproduces a pre-fix panic without first confirming the routing mode. If it turns out to be pure-Gin, the smoke should still be run (the fix is still correct/needed defensively, and the body-preservation regression test still matters), but the handoff must say "did not reproduce a pre-fix panic on this route" rather than implying it did.

## 4. Design (ready for Developer — do not re-litigate insertion point, see D1–D3)

**Fix**, in `pkg/service/http_adapter.go`:

```go
func GinAdapter(callback func(c HttpAdapter) error, logger logger.Logger, localDebug bool) func(*gin.Context) {
	return func(g *gin.Context) {
		if g.Request != nil && g.Request.Body == nil {
			g.Request.Body = http.NoBody
		}
		if err := callback(&ginAdapter{...}); err != nil {
			...
		}
	}
}

func EchoAdapter(callback func(c HttpAdapter) error, logger logger.Logger, localDebug bool) func(c echo.Context) error {
	return func(c echo.Context) error {
		if req := c.Request(); req != nil && req.Body == nil {
			req.Body = http.NoBody
		}
		return callback(&echoAdapter{...})
	}
}
```

Rationale: `req.Body` is `io.ReadCloser` and `http.NoBody` satisfies it (`Read` → immediate `io.EOF`, `Close` → nil) — this is literally what `net/http`'s own server hands a handler for an empty-body request, so every existing unguarded `json.NewDecoder(...).Decode(...)` call site in the fleet gets the same `io.EOF`-on-first-read behavior it already (implicitly) assumes, with zero call-site changes. No change to `WithVanillaHandler`'s wiring (out of scope, confirmed PM's call — see §5).

**Test file**: new `pkg/service/http_adapter_test.go`. The existing `helpers_test.go` pattern (`bodyAdapter`, `newBodyAdapter`) mocks the `HttpAdapter` interface directly and can't be reused as-is — `GinAdapter`/`EchoAdapter` take concrete `*gin.Context` / `echo.Context`, not `HttpAdapter`, so the new tests need real framework contexts:

- Gin: `gin.CreateTestContext(httptest.NewRecorder())`, then `c.Request = httptest.NewRequest(http.MethodPost, "/x", nil)` — confirmed via `net/http`'s `NewRequestWithContext` semantics that a `nil` `io.Reader` body argument leaves `Request.Body == nil` (the body type-asserts to `io.ReadCloser`, fails since the interface itself is nil, and the `body != nil` branch that would wrap it never fires) — so `httptest.NewRequest(..., nil)` really does reproduce the pre-fix nil-body condition.
- Echo: `e := echo.New(); c := e.NewContext(httptest.NewRequest(http.MethodPost, "/x", nil), httptest.NewRecorder())`.

Required tests (per brief + PM's AC2/AC3):
1. `TestGinAdapter_NilBody_NormalizesToNoBody` / `TestEchoAdapter_NilBody_NormalizesToNoBody` — run the wrapped handler with a callback that does `io.ReadAll(c.Request().Body)`; assert no panic, `0` bytes, `err == nil` (NoBody's `Read` returns `io.EOF` which `io.ReadAll` swallows as success).
2. `TestGinAdapter_NonEmptyBody_PreservedByteForByte` / Echo equivalent — request built with `strings.NewReader("...")` body; assert the callback observes the exact bytes, unchanged, not truncated, not double-read.

## 5. Scope carried forward unchanged from PM (not re-litigated)

- `forge-conductor`'s `normalizeBodyMiddleware` — untouched this slice (gated on all 6 consumers adopting the new SDK version).
- `WithVanillaHandler` its-felix wiring — NICE-TO-HAVE only, no confirmed consumer.
- Bumping `forge-sessions`/`forge-runtime`/`forge-notifier`/`forge-storage` — out of scope, 0 unguarded sites surveyed.
- Safe version-uptake plan — PM's table in their handoff §5 stands; I have nothing to add beyond flagging D7 above as a pre-condition on the aigateway leg of that plan.

## 6. Acceptance criteria — status handed to Developer (none are PASS yet; this is a design turn)

| AC# | Criterion (from brief/PM) | Status | Evidence / reason |
|---|---|---|---|
| AC1 | Nil `Body` normalized to `http.NoBody` in `GinAdapter`/`EchoAdapter` before handler runs | **PENDING — Developer** | Design confirmed sound against real source (§3 D1–D3, §4); not yet coded. |
| AC2 | Unit test: body-less POST never panics, observes `http.NoBody` behavior | **PENDING — Developer** | Test plan specified in §4 against real framework constructors; not yet written/run. |
| AC3 | Unit test: non-empty POST body preserved byte-for-byte | **PENDING — Developer** | Same as AC2. |
| AC4 | `go test ./...` green in SDK | **PENDING — Developer** | No code changed yet, nothing to run. |
| AC5 | `forge-aigateway` go.mod bumped, `go build ./...` succeeds | **PENDING — Developer/DevOps** | Current pin confirmed: `v0.0.0-20260930194956-2a8b117cc526` (§3 D6). New pseudo-version only exists once the SDK fix is committed. |
| AC6 | Live smoke: body-less POST to a non-conductor optional-body endpoint returns normal response | **PENDING — QA/DevOps, blocked on D7** | `allowance_handler.go`'s `POST /api/internal/allowance-snapshot` is a real, confirmed unguarded decode site (§3 D5) — but Developer must first confirm (grep, real clone) which routing mode forge-aigateway runs before claiming this route can reproduce a pre-fix panic (§3 D7). If Gin/API-Gateway-only, pick/verify a different angle or state explicitly that this route is defense-in-depth rather than a reproducible-today panic. |
| AC7 | `forge-conductor`'s `normalizeBodyMiddleware` left untouched | **N/A this turn** | No `forge-conductor` files touched by Architect or planned for touch by Developer per this slice's scope. |
| AC8 | Safe-uptake plan documented, not actioned | **PASS** | PM's handoff §5 table stands; carried forward unchanged, with D7 appended as a pre-condition on the aigateway row. |

## 7. Tests run

None — no code was mutated this turn (design/handoff only).

**Empty-PR-OK:** yes — this is an Architect design/verification turn against the brief; no source diff is expected from this role this turn, and the AC table above carries real evidence (quoted source, confirmed go.mod pin, confirmed handler code) rather than hand-waved claims.

**Verdict:** signoff