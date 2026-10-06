[lambda-nil-request-body-panics-handlers] Body-less POSTs panic on Lambda — nil req.Body, fleet-wide
Fix in go-aws-lambda-sdk: the Lambda HTTP adapter constructs requests with req.Body == nil for empty payloads, unlike net/http's http.NoBody invariant; unguarded json decoders then panic. Normalise empty bodies to http.NoBody at SDK request construction, not one middleware per service. Add focused tests for body-less POST and non-empty POST (body preserved). Fleet blast radius: all six services use this SDK; confirm at least one dependent service builds against the change and plan safe version uptake. Definition of done: SDK fix and tests; dependent service build; a body-less POST to an optional-body endpoint on a NON-conductor service returns its normal response in a live smoke. The conductor's local normalizeBodyMiddleware is to be removed only after all consumers deploy the fixed SDK; do not remove it prematurely. Three exposed aigateway decode sites are listed in the roadmap item for smoke targeting. Do not claim the fleet-wide live DoD until dependent services have adopted the version.

── Source from the workflow that triggered this (context only — it is from a DIFFERENT run, often a different repo; do NOT reuse its commit SHA, branch, or file paths as your own) ──
All edits accounted for. Handoff below.

---

# Roadmap Driver — handoff

## 1. Commit + branch identity
No code mutations — this turn edited only `docs/roadmap/` (index + item files + driver log), which is this character's normal roadmap-maintenance output, not source code. Files will be committed by the engine on this run's branch: `«branch-from-triggering-run-redacted»`.

## 2. Files touched

| Path | What changed |
|---|---|
| `docs/roadmap/README.md` | Flipped `isolated-gate-cannot-deploy-meeting-brain` → `🔒 BLOCKED`; flipped `concierge-call-next-actions` and `lambda-nil-request-body-panics-handlers` → `🚧 IN PROGRESS`; updated `briefing-templates-as-data` next-step text |
| `docs/roadmap/items/isolated-gate-cannot-deploy-meeting-brain.md` | `status: next→blocked`, `dispatch: auto→manual`, cleared `dispatched:`, set `blocked_on:`, added History line (PR #1517 merged, S1 live-verified pre-merge) |
| `docs/roadmap/items/concierge-call-next-actions.md` | `status: next→in-progress`, set `dispatched: "2026-10-06"`, added History line (dispatched to Main Forge SDLC against forge-concierge) |
| `docs/roadmap/items/lambda-nil-request-body-panics-handlers.md` | `status: next→in-progress`, set `dispatched: "2026-10-06"`, added History line (dispatched to Main Forge SDLC against go-aws-lambda-sdk) |
| `docs/roadmap/items/briefing-templates-as-data.md` | Added PM scope decision (S1 narrowed to a single conductor-owned Mongo row for `conciergeWelcomeBriefing`, PATCH-only, seeded default), `next:` updated, History line noting the fan-out-cap miss |
| `docs/roadmap/driver-log/2026-10-06.md` | Appended this run's note (newest-first) |

## 3. Acceptance criteria verification

| AC# | Criterion | Status | Evidence |
|---|---|---|---|
| AC1 | Reconcile 🚧 IN PROGRESS slices against GitHub | PASS | `playwright-e2e-never-runs-in-ci`: PR #523 open, titled `[escalated]`, not merged — left in-progress, no action needed (correctly non-stale, <4h, real PR exists). `isolated-gate-cannot-deploy-meeting-brain`: PR #1517 merged `«sha-from-triggering-run-redacted»` at 2026-10-06T21:49:24Z, confirmed via `gh pr view`. `atrium-portal-memberships-write-surface`: already correctly `blocked`/`manual`, stale `dispatched:` left alone since the block, not abandonment, is the true state. |
| AC2 | Pick next slice(s) off the index, respecting pick order + parallel cap | PASS | 0 live-dispatched runs at pick time → cap allowed 4; picked `concierge-call-next-actions` and `lambda-nil-request-body-panics-handlers` (both `⬜ NEXT`, short-term, table order); attempted a third (`briefing-templates-as-data`) and hit `not_eligible: max follow-up fan-out (2) reached for this run` — a platform cap, not a roadmap-state issue |
| AC3 | Dispatch via `trigger_workflow` to Main Forge SDLC with correct `linked_repo_id` | PASS | `concierge-call-next-actions` → `6aa08f78d043d40d226a5c74` (forge-concierge, verified against dispatch table, not the decoy `forge-concierge-` id); `lambda-nil-request-body-panics-handlers` → `6a1fffa209c64017063fa939` (go-aws-lambda-sdk); both returned `queued:true` |
| AC4 | Mark each dispatched slice IN PROGRESS (item + index, both) | PASS | Both items: `status`, `dispatched:`, `updated:`, History line set; both index rows flipped to 🚧 IN PROGRESS with matching next-step text |
| AC5 | lint.sh exits 0 | PASS | `bash docs/roadmap/lint.sh` → `347 items, 347 index rows, README.md 198 lines — 0 errors, 0 warnings` |
| AC6 | No archive pass performed if gate doesn't open | PASS | Checked `meeting-roster-producer-forge-baas` (closest candidate, ✅ SHIPPED) — still carries an `⬜ NEXT` row for its sibling smoke, so archive gate stays closed; no archive pass run |

## 4. Tests run
Not applicable to this role — no code mutated. Ran `bash docs/roadmap/lint.sh` as the mandated structural check (output above, 0 errors).

## 5. Notes for Architect / downstream
- Two SDLC runs are now live and will need reconciliation on the next Driver pass: `concierge-call-next-actions` (forge-concierge) and `lambda-nil-request-body-panics-handlers` (go-aws-lambda-sdk).
- `isolated-gate-cannot-deploy-meeting-brain` is now correctly `blocked` — do not re-dispatch; it needs an operator to grant `staging-meetingbaas-api-key` to an SDLC character before S3 can proceed.
- `playwright-e2e-never-runs-in-ci` has an **open escalated PR #523** in forge-conductor awaiting operator review (qa→devops gate failed) — flagging for visibility, no action taken by this run since it's correctly non-stale.
- `briefing-templates-as-data` is now fully scoped (PM decision recorded in the item) and ready to dispatch first thing next run — it only missed dispatch this run due to the platform's per-run follow-up cap, not any roadmap-state blocker.

**Empty-PR-OK:** yes — this run only produced roadmap index/item/log edits (no source code), which is the Roadmap Driver's normal per-run output; the diff against `main` is exactly `docs/roadmap/**`.

**Verdict:** signoff
workflowTriggerParentRunId: c77149cb-ca06-4362-9a1b-7ef2ac914222
workflowTriggerRootRunId: c77149cb-ca06-4362-9a1b-7ef2ac914222