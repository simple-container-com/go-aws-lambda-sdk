# DevOps Handoff — Body-less POSTs panic on Lambda (nil `req.Body`, fleet-wide)

## 0. What this turn was actually for

The code is done and QA-verified. My job was the release/rollout leg: how the fix reaches the fleet, in what order, and whether the live DoD can be closed from inside this run. Three substantive findings, each verified against the repos and the live endpoint — not from memory:

1. **This repo needs no deploy and no operator tagging.** Both PM and QA listed "operator must tag the SDK" as step 1. Wrong for this repo — merging to `main` tags it automatically.
2. **The consumer count in the brief is wrong, and so is the uptake assumption.** Nine modules, four SDK generations. The conductor-middleware gate is several slices away, not one.
3. **AC6 is structurally unreachable from this run**, and I confirmed the credential gap against my own manifest rather than re-litigating QA's 401s.

## 1. Commit + branch identity

- **Commits (mine, this turn):** `ad577056e5` (rollout runbook), `a4f3a3a040` (SYSTEM_PROMPT.md knowledge update)
- **Branch:** `forge/workflow/99171cbe-7c27-4976-a999-931b834b5d62/run/b7bda203-23b9-430d-85de-6850e30e9d55`
- **Verified:** `list_repository_commits` on the run branch shows both as the two newest commits, ahead of QA's `95fdd4e88c`. Paths confirmed written at the project's real locations (`docs/rollout/`, repo root), not only inside my handoff.
- **Ticket filed:** [simple-container-com/go-aws-lambda-sdk#17](https://github.com/simple-container-com/go-aws-lambda-sdk/issues/17) (fingerprint `sdk:go-aws-lambda-sdk:nil-body-fix-consumer-uptake`)
- **Deployment recorded:** `status=skipped` — correct status; see §5.

## 2. Files touched

| Path | Lines added / removed | What changed |
|---|---|---|
| `docs/rollout/nil-body-normalisation-uptake.md` | +170/-0 (new, 8655 B) | Release mechanics of this repo, verified 9-consumer pin inventory, ordered uptake plan, conductor-middleware gate, live-smoke recipe + named credential blocker, explicit operational risks + rollback |
| `SYSTEM_PROMPT.md` | +64/-1 (5981 → 9236 B) | Added "Request-body invariant" (why `normalizeRequestBody` exists, every entry point it's wired at, instruction for future entry points), "Releasing and consumer uptake" (calver-tag-on-merge, why consumers pin pseudo-versions not tags), "Known repo-level debt" (v1/v2 `.golangci.yml` mismatch, `welder` `tools` task mutating `go.mod`) |

`SYSTEM_PROMPT.md` is this repo's contributor-knowledge file and it explicitly says *"Always update this SYSTEM_PROMPT.md when gaining new knowledge about the project"* — so updating it is following the repo's own contract, not scope creep.

## 3. Acceptance criteria verification

| AC# | Criterion | Status | Evidence |
|---|---|---|---|
| AC1 | Nil `Body` → `http.NoBody` before the handler chain runs | **PASS** | Re-read the diff myself via `read_commit_diff` on `17cbf80d5b`: `normalizeRequestBody` at `http_adapter.go:+36`, wired at `service.go` (gin `Use` first in chain, echo `Pre`, `withNormalizedBody` on both vanilla paths) and `yandex.go:+120`. Agreed with Developer/QA. |
| AC2 | Body-less POST does not panic; handler observes `http.NoBody` | **PASS** | Verified in the committed diff: `TestGinAdapter_NilBody_NormalizedToNoBody` / `TestEchoAdapter_NilBody_NormalizedToNoBody` assert `http.NoBody` identity + `ErrorIs(decodeErr, io.EOF)`. QA's independent mutation run: 6 tests fail with a real panic when the fix is neutered. |
| AC3 | Non-empty POST body preserved byte-for-byte | **PASS** | `TestGinAdapter_NonEmptyBody_PreservedByteForByte` / echo twin in the diff assert the exact payload (incl. non-ASCII) and `NotEqual(http.NoBody, …)`. |
| AC4 | `go test ./...` green in the SDK | **PASS (inherited, not re-run)** | QA ran it in the checked-out tree: 4/4 packages `ok`, `TEST_EXIT=0`. My turn added no Go source, so the result still holds — stating this plainly rather than re-claiming a run I didn't do. |
| AC5 | Dependent service builds against the fix | **PASS (inherited)** | QA independently reproduced: fresh `forge-aigateway` clone, `go mod edit -replace` → this tree, `go build ./...` exit 0, `go vet ./internal/aigateway/...` exit 0. I re-confirmed the pin it was bumped *from* is still live on `main`: `go.mod` → `v0.0.0-20260930194956-2a8b117cc526`. |
| AC6 | Live smoke: body-less POST to a non-conductor optional-body endpoint returns its normal response | **FAIL — operator-blocked (credential + sequencing)** | Probed live myself, 2026-10-06T23:30Z: `GET https://ai.simple-forge.com/health` → **200** `{"status":"healthy","service":"ai-gateway","version":"2.0.0"}`; `POST /v1/embeddings` no body → **401** `{"error":"missing api key or authorization header"}`; `POST /api/status` → **401**; `POST /` (the only `skipAuthRoutes` entry) → **200** but serves the SPA `<!doctype html>` and decodes no body. Blocker is **SC secret alias `staging-aigateway-api-key`** (`.sc/stacks/forge-aigateway/client.yaml` → `API_KEY`), which my `secret_list` shows I do **not** hold — my manifest grants only `sc_deploy_config`. Second, independent blocker: the deployed aigateway still pins the pre-fix SDK, so the smoke is meaningless until it is bumped and redeployed. Recipe committed, ready to run. |
| AC7 | `forge-conductor`'s `normalizeBodyMiddleware` left untouched | **PASS** | My two commits touch `docs/rollout/` + `SYSTEM_PROMPT.md` in this repo only. Zero `forge-conductor` files, zero `go.mod`/`go.sum` changes, zero changes to any consumer repo. |
| AC8 | Safe-uptake plan documented, not actioned | **PASS — and materially corrected** | Now a committed artifact, not prose in a handoff: `docs/rollout/nil-body-normalisation-uptake.md`. PM's table said "six services, 4 remaining, bump opportunistically". Real figure, from reading every `go.mod`: **nine** modules on **four** SDK generations (§4). Not actioned — no consumer `go.mod` was modified. |

## 4. Rollout findings (the substance of this turn)

### F1 — No deploy exists for this repo, and no operator tagging is needed

`go-aws-lambda-sdk` is a library: no `.sc/` directory, no stack, no deployable artifact, no isolated-stack lane. `.github/workflows/push.yaml` on merge to `main` runs `reecetech/version-increment` (`scheme: calver`, `increment: patch`, `use_api: true`) then `welder deploy -e prod`, whose only action is `welder.yaml`'s `tag-release` task: `git tag $VERSION && git push origin $VERSION`. **Merging the PR tags the release automatically.** Delete "operator must tag the SDK" from the follow-up list.

Latest tag is `2026.9.2` at commit `2a8b117cc526` (GitHub tags API). Under calver a month rollover resets the release digit, so the merge tag will be **`2026.10.1`** (scheme per the action's own README — verified, not assumed).

### F2 — The tag is not what consumers consume

`2026.10.1` has no `v` prefix, so it is **not a valid Go module version**. Every consumer in the org pins a pseudo-version `v0.0.0-<timestamp>-<sha>`. The uptake command is `go get …@<merge-sha> && go mod tidy`. Anyone who plans the rollout around "bump to the new tag" will waste a cycle.

### F3 — Nine consumers, four generations — the conductor gate is not one slice away

Verified by reading each `go.mod` today:

| Consumer | Pinned SDK | = release | Has `2a8b117c` ReadBody fix? |
|---|---|---|---|
| forge-conductor | `…-2a8b117cc526` | 2026.9.2 | yes |
| forge-aigateway | `…-2a8b117cc526` | 2026.9.2 | yes |
| forge-sessions | `…-143180a4d61d` | 2026.5.2 | no |
| forge-notifier | `…-143180a4d61d` | 2026.5.2 | no |
| forge-concierge | `…-143180a4d61d` | 2026.5.2 | no |
| vpr-math-tutor | `…-143180a4d61d` | 2026.5.2 | no |
| forge-runtime | `…-dddcae366726` | 2025.12.1 | no |
| observatory | `…-dddcae366726` | 2025.12.1 | no |
| forge-storage | `…-7ed5ace43746` | 2025.1.1 | no |

Three consequences, all recorded in the runbook and the ticket:
- The brief's "all six services" undercounts by three (`vpr-math-tutor`, `observatory`, `forge-storage` were not on anyone's list).
- Seven of nine are also missing the earlier malformed-body 400 fix — they inherit two fixes, not one.
- **`forge-conductor`'s `normalizeBodyMiddleware` must be treated as permanent until that table is uniform.** Its removal is its own slice with its own smoke; it is not a drive-by after one bump. Ordered plan: aigateway → the three 2026.5.2 services → the four stale ones (a slice each, expect transitive drift) → conductor last.

### F4 — Operational risks, flagged early

- **Deploy-on-merge means no pre-merge gate.** Each consumer deploys `staging` straight off `main` (`deploy-forge-aigateway.yml`'s `paths-ignore` only skips `.workflow/**` and `docs/roadmap/**`, so a `go.mod` change *does* deploy). Verification is post-merge — have the smoke command in hand before merging.
- **`go mod tidy` on a 10-month-old pin moves more than one line.** Review the whole `go.mod`/`go.sum` diff; split anything outside the SDK into its own PR.
- **Rejected shortcut:** pinning a consumer to an unmerged SDK commit. The module proxy would serve it (public repo), and it would get a staging deploy off a commit that never passed review. Sequence is merge-then-bump, always.
- **Rollback** is a `go.mod` revert in the consumer — one PR, no infra or state involved. The SDK change is additive, so a rollback reinstates the panic and breaks nothing else.
- **QA's Q2 confirmed independently:** there is no isolated-stack path for a library repo, so Gate A genuinely cannot gate this change. Not a skipped gate.

## 5. Why `report_deployment` is `skipped`, not `failed`

`skipped` is the accurate token: there is nothing in this repo to deploy. A library has no stack, so "deploy failed" would be a fabricated failure against a deployment that was never possible. The notes field carries the live-probe evidence (`/health` 200, the three 401s), the named secret alias, and the runbook path, so a reviewer sees on the PR exactly what is outstanding and why.

## 6. Tests run

No Go source mutated this turn — my two commits are markdown. I did not re-run `go test ./...`; AC4/AC5 are inherited from QA's output and labelled as such rather than re-claimed.

Live verification I ran myself (full status/body in AC6):

```
GET  https://ai.simple-forge.com/health        → 200  {"status":"healthy","service":"ai-gateway","version":"2.0.0",...}
POST https://ai.simple-forge.com/v1/embeddings → 401  {"error":"missing api key or authorization header"}
POST https://ai.simple-forge.com/api/status    → 401  {"error":"missing api key or authorization header"}
POST https://ai.simple-forge.com/              → 200  <!doctype html> (SPA; decodes no body)
```

Repo facts verified via authenticated repo tools: `push.yaml`, `welder.yaml`, `.golangci.yml`, `tools.go`, tags API, and the nine consumer `go.mod` files. No transient failures — no retries needed this turn.

## 7. Operator actions required (revised — one fewer step than prior handoffs claimed)

1. **Merge this PR.** The SDK tags itself (`2026.10.1`); no manual `git tag`.
2. **Bump `forge-aigateway`**: `go get github.com/simple-container-com/go-aws-lambda-sdk@<merge-sha> && go mod tidy`, merge to `main` — it auto-deploys to staging.
3. **Grant SC secret alias `staging-aigateway-api-key`** to a QA/DevOps character, **or** run the smoke by hand:
   ```bash
   curl -sS -w '%{http_code}\n' -X POST \
     -H "Authorization: Bearer $AIGATEWAY_API_KEY" \
     https://ai.simple-forge.com/v1/embeddings
   # PASS: 400 {"error":"invalid request body"}   FAIL: 502 / timeout / panic
   ```
4. **Then** work issue #17 in order. Do **not** delete conductor's `normalizeBodyMiddleware` until the pin table is uniform.

## 8. Follow-ups I did not action

- `.golangci.yml` v1-format vs v2.x binary. Worth noting CI is **not** broken — `welder.yaml`'s `linters` task runs `bin/golangci-lint` built from the `go.mod`-pinned `v1.64.8`, so the gate works in CI and only ad-hoc/agent runs fail. That narrows QA's "silently non-functional since the v2 upgrade": the real cost is agent/local friction, not an unguarded `main`. Recorded in `SYSTEM_PROMPT.md`.
- `welder.yaml`'s `tools` build step runs `go get` + `go mod tidy`, so a release build can mutate `go.mod`. Pin instead. Recorded.
- aigateway's 5 decode sites should use `ReadBody` for structured errors + request UID (Developer/QA's point; I concur, out of scope).

**Chain continuation:** the brief did not ask to continue to the next slice, so `chainContinue=false` — no `trigger_workflow` call.

**Empty-PR-OK:** no — this run carries a real source diff (+408/-3 across 5 `pkg/service/` files from Developer+QA) plus my two documentation commits.

**Verdict:** signoff