All paths verified as my commits in this run, pushed to the run branch.

# Developer Handoff — Body-less POSTs panic on Lambda (nil `req.Body`, fleet-wide)

## 1. Commit + branch identity

- **Commits:** `17cbf80d5b` (fix + unit tests), `4df4ec8cd4` (end-to-end Lambda-event tests) — HEAD = `4df4ec8cd41f2fe4ac2e3af6e457f2792c744450`
- **Branch:** `forge/workflow/99171cbe-7c27-4976-a999-931b834b5d62/run/b7bda203-23b9-430d-85de-6850e30e9d55`
- **Pushed:** yes — `4304929..4df4ec8`, confirmed against `origin/<branch>`. PR auto-opened by the engine at handoff.

## 2. Files touched

| Path | Lines added / removed | What changed |
|---|---|---|
| `pkg/service/http_adapter.go` | +52/-0 | Added `normalizeRequestBody`, `normalizeBodyGinMiddleware`, `normalizeBodyEchoMiddleware`, `withNormalizedBody`; guards in `GinAdapter`, `EchoAdapter`, and `ginRouter.Use` |
| `pkg/service/service.go` | +11/-2 | Install `normalizeBodyGinMiddleware()` first in the gin chain; `echoRouter.Pre(normalizeBodyEchoMiddleware())`; wrap `vanillaHandler` with `withNormalizedBody` (both `http.Server` + `NewVanillaAdapter`) |
| `pkg/service/yandex.go` | +5/-0 | `normalizeRequestBody(r)` in `yandexTriggerHandler` before its direct `io.ReadAll(r.Body)` |
| `pkg/service/http_adapter_test.go` | +216/-0 | 10 new tests: nil-body + non-empty-body for both adapters, both router middlewares, `withNormalizedBody`, `ReadBody` interaction, nil-request safety |
| `pkg/service/lambda_body_test.go` | +122/-0 | 3 new tests driving real empty-body `LambdaFunctionURLRequest` events through the real its-felix adapter |

**Deviation from the Architect's design (flagged, not silent):** the design specified two guards in `GinAdapter`/`EchoAdapter`. Those alone leave three reachable gaps I found reading the code, so I added router-level middleware as the primary fix and kept the adapter guards as defence-in-depth:
1. `ginRouter.Use` (`http_adapter.go:276`) builds its adapter via `newGinAdapter`, **not** `GinAdapter` — so all SDK + consumer middleware bypassed the proposed guard.
2. Swagger and `NoRoute` handlers are registered directly on the engine, bypassing it too.
3. `WithVanillaHandler` and `yandexTriggerHandler` bypass both routers; the latter reads `r.Body` directly at `yandex.go:128`.

## 3. Acceptance criteria verification

| AC# | Criterion | Status | Evidence |
|---|---|---|---|
| AC1 | Nil `Body` → `http.NoBody` before the handler chain runs | **PASS** | `http_adapter.go:38-40` (`normalizeRequestBody`), wired at `service.go:170` (gin, first in chain), `service.go:163` (echo `Pre`), plus guards at `http_adapter.go:236,247,277` |
| AC2 | Body-less POST does not panic; handler observes `http.NoBody` | **PASS** | `TestGinAdapter_NilBody_NormalizedToNoBody`, `TestEchoAdapter_NilBody_NormalizedToNoBody`, `TestEchoRouter_EmptyBodyEvent_DecodesWithoutPanic` — decoder returns `io.EOF`, no panic. **Verified to genuinely catch the bug:** with the fix neutered, 5 tests fail with a real panic inside `json.Decode` (trace: `decodeHandler.1 → GinAdapter.2 → runtime/panic.go:783`) |
| AC3 | Non-empty POST body preserved byte-for-byte | **PASS** | `TestGinAdapter_NonEmptyBody_PreservedByteForByte`, `TestEchoAdapter_NonEmptyBody_PreservedByteForByte`, `TestEchoRouter_NonEmptyBodyEvent_Preserved` (incl. non-ASCII payload) |
| AC4 | `go test ./...` green in the SDK | **PASS** | 4/4 packages `ok` — output in §4 |
| AC5 | Dependent service builds against the fix | **PASS** | `forge-aigateway` cloned, `go mod edit -replace` → this working tree: `go build ./...` exit 0, `go vet ./internal/aigateway/...` exit 0 (also compiles its test code). Note: needed `mkdir internal/ui/dist` for a pre-existing `embed` pattern unrelated to this change |
| AC6 | Live smoke: body-less POST to a non-conductor optional-body endpoint returns its normal response | **FAIL — operator-blocked** | **Two independent blockers.** (a) **Credential:** every decode route sits behind the SDK's `apiKeyAuthMiddleware` (`service.go:209`); live `POST /v1/embeddings` with no body → `401 {"error":"missing api key or authorization header"}`. Needs secret alias **`staging-aigateway-api-key`** (`client.yaml:73`), which I do not hold. The only `skipAuthRoutes` entry is `/`, whose handler serves the SPA and decodes no body — `POST /` → 200 proves liveness but exercises nothing. (b) **Not yet deployed:** `ai.simple-forge.com` still runs the old SDK pin; the smoke is only meaningful after aigateway's `go.mod` is bumped and redeployed. Not hand-waved, not narrowed |
| AC7 | `forge-conductor`'s `normalizeBodyMiddleware` left untouched | **PASS** | `git diff --numstat` lists only the 5 `pkg/service/` paths above; zero `forge-conductor` files, zero `go.mod`/`go.sum` changes in this repo |
| AC8 | Safe-uptake plan documented, not actioned | **PASS** | PM §5 stands; D7 now resolved below, and the aigateway row is corrected from 3 to 5 unguarded sites |

### D7 — resolved (the Architect's open question)

`forge-aigateway` calls `service.New(ctx, WithRoutes, WithSkipAuthRoutes("/"))` at `internal/aigateway/service.go:383` — it sets neither option explicitly, so routing is **env-driven** (`service.go:86,115`). Its deploy config sets **`lambdaInvokeMode: RESPONSE_STREAM`** (`.sc/stacks/forge-aigateway/client.yaml:22`) → `UseResponseStreaming(true)` → the **echo/its-felix path, the genuinely vulnerable one**.

So this is a real production defect in aigateway, **not** defence-in-depth. I proved the upstream behaviour rather than assuming it: `TestItsFelixAdapter_EmptyBodyEvent_ProducesNilBody` asserts a real empty-body Function URL event through the real adapter yields `Body == nil`, and it passes.

**Correction to the brief/PM:** aigateway has **5** unguarded `json.NewDecoder(c.Request().Body)` sites, not 3 — the two extra are `internal/service/handlers/aigateway/model_tiers_edit.go:61` and `:111`. Worth re-checking the other services' zero-site survey on the same pattern.

## 4. Tests run

```
$ go build ./... && go vet ./... && gofmt -l pkg/ && gofumpt -l pkg/
BUILD OK / VET OK / FMT OK   (no files listed by either formatter)

$ go test ./...
ok  github.com/simple-container-com/go-aws-lambda-sdk/pkg/awsutil      (cached)
ok  github.com/simple-container-com/go-aws-lambda-sdk/pkg/logger       (cached)
ok  github.com/simple-container-com/go-aws-lambda-sdk/pkg/service      0.011s
ok  github.com/simple-container-com/go-aws-lambda-sdk/pkg/util/retry   (cached)
```

All 13 new tests pass individually (`--- PASS` for each of `TestGinAdapter_NilBody_NormalizedToNoBody`, `TestEchoAdapter_NilBody_NormalizedToNoBody`, `TestGinAdapter_NonEmptyBody_PreservedByteForByte`, `TestEchoAdapter_NonEmptyBody_PreservedByteForByte`, `TestGinAdapter_NilBody_ReadBodyStillRejects`, `TestNormalizeBodyGinMiddleware_CoversPlainHandlers`, `TestNormalizeBodyEchoMiddleware_CoversPlainHandlers`, `TestWithNormalizedBody`, `TestNormalizeRequestBody_NilRequestIsSafe`, `TestItsFelixAdapter_EmptyBodyEvent_ProducesNilBody`, `TestEchoRouter_EmptyBodyEvent_DecodesWithoutPanic`, `TestEchoRouter_NonEmptyBodyEvent_Preserved`).

**Mutation check** (the fix neutered to `_ = r`): 5 tests fail with a genuine panic, confirming they test behaviour rather than restating the implementation. Fix restored and re-verified before committing.

**Dependent service (AC5):**
```
$ cd /tmp/ag && go mod edit -replace ...go-aws-lambda-sdk=<this tree> && go build ./...
BUILD_EXIT=0
$ go vet ./internal/aigateway/...
VET_EXIT=0
```

**Linter:** `golangci-lint run` fails with `can't load config: unsupported version of the configuration: ""` — **pre-existing**, not mine: the repo's `.golangci.yml` is v1-format while the installed binary is v2.14.0, and it fails identically on `./pkg/logger/...` which I never touched. Ran the underlying linters directly instead: `staticcheck`, `errcheck`, `ineffassign`, `gofumpt` all clean on my files. `staticcheck`'s only finding is pre-existing in `helpers_test.go:187` (SA1012, nil Context), untouched by me.

**Not run:** `make swag` — no annotated handler or struct changed (no `Makefile` in this repo; no swagger drift possible). aigateway's own test suite — it needs Docker/testcontainers + Mongo, and the brief scopes AC5 to "builds".

## 5. Next steps / what the operator must decide

**To close AC6**, in order:
1. Tag the SDK (patch bump — nil→`NoBody` is backward-compatible, no consumer change needed).
2. Bump `forge-aigateway`'s `go.mod` off `v0.0.0-20260930194956-2a8b117cc526` to the new version, deploy to staging.
3. Smoke `POST /v1/embeddings` on `ai.simple-forge.com` with **no body** and a valid `API_KEY` — expect `400 {"error":"invalid request body"}` (the handler's own `io.EOF` branch), **not** a 502/panic. Requires secret alias **`staging-aigateway-api-key`**.

Step 3 needs a grant I do not hold. QA holds `forge_api_key` (conductor), which is a **different** credential and will not authenticate against aigateway — so unless QA also holds `staging-aigateway-api-key`, this needs the operator, not the next character.

**Worth a follow-up slice:** aigateway's 5 unguarded decode sites return a bare 400 on an empty body. That is now correct behaviour rather than a panic, but `ReadBody` (which gives a structured error + request UID) would be better. Out of scope here.

**Empty-PR-OK:** no — this run produced a real source diff (+406/-2 across 5 files in `pkg/service/`).

**Verdict:** signoff