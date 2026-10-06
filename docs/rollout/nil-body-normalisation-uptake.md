# Uptake runbook — empty Lambda request body → `http.NoBody`

Scope: how the fix in `pkg/service/http_adapter.go` (`normalizeRequestBody` +
router middleware, see `.workflow/b7bda203-23b9-430d-85de-6850e30e9d55/`) reaches
the fleet, in what order, and what has to be true before
`forge-conductor`'s local `normalizeBodyMiddleware` may be deleted.

Written 2026-10-06 by DevOps (William Smith) on run
`b7bda203-23b9-430d-85de-6850e30e9d55`. Every claim below was verified against
the repos/endpoints cited, not from memory.

---

## 1. How a release of this repo actually happens

This repo is a **Go library**: there is no deployable artifact, no stack, no
`.sc/` config, and no isolated-stack lane. Uptake is a `go.mod` bump in each
consumer, nothing more.

Release path (`.github/workflows/push.yaml`, verified on this branch):

```
push to main
  └─ job prepare : reecetech/version-increment (scheme=calver, increment=patch, use_api=true)
  └─ job build   : welder make        → tasks tools, linters, test
                   welder deploy -e prod → task tag-release → git tag $VERSION && git push origin $VERSION
```

- `welder.yaml`'s `tag-release` is the **only** thing the "deploy" step does. Merging
  the PR to `main` therefore tags and pushes the release **automatically** — no
  operator `git tag` step is required. (Prior handoffs list "operator must tag the
  SDK" as step 1; that is wrong for this repo.)
- Latest tag today is `2026.9.2` (commit `2a8b117cc526`, GitHub tags API). Under
  calver, a month change resets the release digit, so the merge tag will be
  **`2026.10.1`** (scheme documented at
  `github.com/reecetech/version-increment` README — "If the current latest normal
  version is not the current year and month, then the year and month digits will be
  set to the current year and month, and the release digit will be reset to 1").
- ⚠ **The tag is not what consumers consume.** `2026.10.1` has no `v` prefix, so it
  is not a valid Go module version. Every consumer in the org pins a
  **pseudo-version** instead — `v0.0.0-<utc-timestamp>-<12-char-sha>`. The uptake
  command is therefore:

  ```bash
  go get github.com/simple-container-com/go-aws-lambda-sdk@<merge-commit-sha>
  go mod tidy
  ```

  which resolves to `v0.0.0-<date>-<sha>` with no tag involved. The tag matters only
  for human change tracking.

## 2. Consumer pin inventory (verified 2026-10-06 by reading each repo's `go.mod`)

The brief says "all six services use this SDK". The real number is **nine**
modules, and they sit on **four different** SDK generations:

| Consumer | Pinned SDK pseudo-version | = release | Has `ReadBody` 400-fix (`2a8b117c`)? |
|---|---|---|---|
| `forge-conductor` | `v0.0.0-20260930194956-2a8b117cc526` | 2026.9.2 | yes |
| `forge-aigateway` | `v0.0.0-20260930194956-2a8b117cc526` | 2026.9.2 | yes |
| `forge-sessions` | `v0.0.0-20260527071046-143180a4d61d` | 2026.5.2 | no |
| `forge-notifier` | `v0.0.0-20260527071046-143180a4d61d` | 2026.5.2 | no |
| `forge-concierge` | `v0.0.0-20260527071046-143180a4d61d` | 2026.5.2 | no |
| `vpr-math-tutor` | `v0.0.0-20260527071046-143180a4d61d` | 2026.5.2 | no |
| `forge-runtime` | `v0.0.0-20251216122731-dddcae366726` | 2025.12.1 | no |
| `observatory` | `v0.0.0-20251216122731-dddcae366726` | 2025.12.1 | no |
| `forge-storage` | `v0.0.0-20250103123833-7ed5ace43746` | 2025.1.1 | no |

Each pinned SHA is the commit a release tag points at (cross-checked against the
tags API), i.e. consumers bump release-to-release, not to arbitrary commits. Keep
that convention.

**Consequence for the conductor-middleware gate:** the gate is "all consumers
deployed on the fixed SDK". With seven of nine modules more than four months
behind, that gate is **not one slice away**. Treat `forge-conductor`'s
`normalizeBodyMiddleware` as permanent until the table above is uniformly on the
fix — and delete it in its own slice with its own smoke, never as a drive-by.

## 3. Ordered uptake plan

Do not batch these. One PR per consumer, each one deploys independently.

| # | Consumer | Why this order | Trigger that deploys it |
|---|---|---|---|
| 1 | `forge-aigateway` | Only module with confirmed **reachable** panic sites (5 unguarded `json.NewDecoder(c.Request().Body)`) **and** on the vulnerable echo/its-felix path (`.sc/stacks/forge-aigateway/client.yaml` → `lambdaInvokeMode: RESPONSE_STREAM`). Closes the live DoD. | push to `main` → `.github/workflows/deploy-forge-aigateway.yml` (auto, `paths-ignore` only skips `.workflow/**` + `docs/roadmap/**`, so a `go.mod` change deploys) |
| 2 | `forge-sessions`, `forge-notifier`, `forge-concierge` | Same 2026.5.2 generation; pick up both this fix and the `2a8b117c` `ReadBody` 400-fix they are missing. Low risk, zero source change. | each repo's own deploy workflow on `main` |
| 3 | `forge-runtime`, `forge-storage`, `observatory`, `vpr-math-tutor` | Oldest pins — expect unrelated drift (deps, Go toolchain) to surface in the bump. Budget a real slice each; do **not** fold into a "trivial bump" PR. | each repo's own deploy workflow on `main` |
| 4 | `forge-conductor` | Last. Bump only. **Deleting `normalizeBodyMiddleware` is a separate slice** gated on 1–3 being deployed and verified. | push to `main` |

Operational risk, stated explicitly:

- **Risk: a bump drags in unrelated transitive upgrades.** `go get <sha>` + `go mod tidy`
  on a 10-month-old pin will move more than one line. Review the full `go.mod`/`go.sum`
  diff; if anything outside the SDK moves, split it into its own PR.
- **Risk: deploy-on-merge means no staging gate.** Every consumer here deploys
  `staging` straight off `main`. The verification step is therefore **post-merge**, not
  pre-merge — have the smoke command ready before merging, not after.
- **Risk: pinning staging to an unmerged SDK commit.** Technically possible (the repo is
  public; the proxy serves any reachable commit) and explicitly **rejected** here.
  Consumers must only ever pin a commit that is on SDK `main`. Sequence: merge the SDK
  PR → read the merge SHA → bump the consumer.
- **Rollback:** revert the consumer's `go.mod`/`go.sum` to the previous pseudo-version and
  merge; the deploy workflow redeploys the previous behaviour. The SDK change is
  additive (`nil` → `http.NoBody`), so a rollback reinstates the panic but breaks nothing
  else. No infra/state rollback is involved.

## 4. Live smoke that closes the fleet DoD

Must run **after** step 1 of the plan above is deployed, and must target a
non-conductor service (per the brief). Target host is live today:

```bash
# liveness — verified 2026-10-06T23:30Z, HTTP 200
curl -sS https://ai.simple-forge.com/health
# {"status":"healthy","service":"ai-gateway","version":"2.0.0",...}

# the smoke: body-less POST to an optional-body decode site
curl -sS -o /dev/stderr -w '%{http_code}\n' -X POST \
  -H "Authorization: Bearer $AIGATEWAY_API_KEY" \
  https://ai.simple-forge.com/v1/embeddings
```

- PASS: `400 {"error":"invalid request body"}` — the handler's own `io.EOF` branch.
- FAIL: `502`, a Lambda timeout, or a panic stack in CloudWatch.

`$AIGATEWAY_API_KEY` is the value of SC secret `staging-aigateway-api-key`
(`.sc/stacks/forge-aigateway/client.yaml` → `API_KEY`). **No workflow character in
this run holds that alias** — confirmed by this run's credential manifest, which
grants only `sc_deploy_config`. Unauthenticated/other-credential probes all 401 at
the SDK's `apiKeyAuthMiddleware`, which runs *before* every decode site, so there is
no auth-free route to the fix (QA finding Q1). The operator must either grant the
alias to a QA/DevOps character or run the two commands above by hand.

Do **not** substitute an isolated stack: `deploy-isolated-stack.yml` takes per-service
branch inputs only (no SDK input — it is a library), so an isolated stack would build
aigateway against whatever SDK its `go.mod` pins and prove nothing (QA finding Q2).

## 5. Also worth fixing while in the area (not actioned)

- `.golangci.yml` is v1-format while the installed `golangci-lint` is v2.x, so the
  aggregated lint gate exits non-zero on config load for every PR — i.e. it has been a
  no-op since the v2 upgrade. `welder.yaml`'s `linters` task runs `bin/golangci-lint`
  built from the `go.mod`-pinned `v1.64.8`, so CI itself still lints; only ad-hoc local
  and agent runs break. Migrate the config in its own slice.
- `welder.yaml`'s `build` steps are `tools → linters → test`: the `tools` task runs
  `go get` + `go mod tidy` at build time, which can move `go.mod` during a release
  build. Pin the toolchain deps instead, in its own slice.
