# NewAPI gateway hardening — cloud recovery

Status: implementation in progress; not a production release. This branch is based on master `a0bc201cc70e52e8a1fda3059b6c9d14ad8c8f89`.

The earlier uncommitted Windows worktree is unavailable because the company computer tunnel is offline. Changes on this branch are reconstructed/reviewed from the retained task evidence, not a claim that the offline worktree was committed byte-for-byte. Do not reset that worktree when it reconnects; reconcile its diff against this branch.

Scope: only `1057300248/oai-prism`. No NewAPI code changes, production deployment, account imports, or paid/live upstream inference are authorized by this workflow.

## Acceptance

- Explicit gateway-only mode, public API keys never grant admin/raw-proxy/local-filesystem access.
- Stateless requests by default. Persisted Responses must use authenticated ownership, immutable snapshots, bounded size/TTL, explicit failure on missing state.
- Stable public response/item IDs; monotonic Responses events; success/failure/cancellation are distinct terminal states.
- Account acquisition/setup/start/poll share one deadline. No accepted generation is replayed on a different account.
- Token usage provenance is explicit. Missing cache/reasoning counts are unknown, not invented zeroes.
- Unsupported upstream semantics are documented and fail clearly, not silently ignored.
- Full Go tests, race, vet/build, SDK contract tests and integration tests run in GitHub Actions without real credentials.

## Fork review (2026-10-03)

The checked default heads are:

- `devImpChen/oai-prism`: `2b69c4dc9a20ce0d6c4bd535e33a168b941c436c`; an older upstream snapshot, not evidence of complete native compatibility.
- `xuseny/oai-prism`: `f3d4c3cd3f2c3feb3885f55d3f983f22ae93f24b`; older stop-script change.
- `EmpFish01/oai-prism`: `ef8854f223b83eac49cbbfe20866f8ed8803a01a`; browser sidecar, login wizard and portable/session-scoped startup improvements. Inspected `internal/facade/responses.go`: several known public parameters are still consumed without execution, and private upstream response IDs are directly accepted. Deployment completeness is not multi-tenant API completeness.

No fork code is copied wholesale; upstream attribution remains unchanged. GitHub metadata did not identify a license for the source project, so do not describe this as an MIT-licensed product or grant a new license on other authors' work.

Primary protocol references:
- https://developers.openai.com/api/docs/guides/function-calling
- https://developers.openai.com/api/docs/guides/streaming-responses
- https://developers.openai.com/api/docs/guides/structured-outputs
- https://docs.newapi.pro/en/docs/api/ai-model/chat/openai/createresponse

Live Prism availability, official tokenizer equivalence, real NewAPI settlement and throughput are separate acceptance gates; mock CI does not certify them.
