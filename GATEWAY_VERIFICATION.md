# Gateway verification record

Date: 2026-10-03. Target repository: `1057300248/oai-prism`.

## Source identity

- Base: `a0bc201cc70e52e8a1fda3059b6c9d14ad8c8f89`.
- Feature branch: `codex/newapi-native-hardening-20261003`.
- Final implementation/test source before this documentation-only commit: `dd41ffb8006e12cdfe79f329960f21d44c1c2d78`.
- Review: https://github.com/1057300248/oai-prism/pull/1
- Setup and compatibility boundaries: [NEWAPI_GATEWAY.md](NEWAPI_GATEWAY.md).
- Config profile: [configs/config.newapi.example.yaml](configs/config.newapi.example.yaml).

The disconnected Windows worktree was not committed byte-for-byte. This is the cloud-reconstructed implementation, verified on GitHub Actions. Master and production services remain unchanged.

## Checks and evidence

The retained workflows are read-only with respect to repository contents:

1. `CI`: existing Go vet/test/race/build, Linux amd64/arm64 compilation, Dashboard TypeScript and Vite build.
2. `Gateway Cloud Verification`: bounded full Go suite, machine-readable per-test results, race suite, production build and source-identifying artifact.
3. `Official SDK Contract`: pinned official Python `openai==3.24.0` and JavaScript `openai@7.27.0`, using a localhost-only deterministic fixture with no real model credentials.

Inspect the workflow runs for the exact PR head; cancelled intermediate runs are not passing evidence. Each final report must distinguish implementation-source verification from the later documentation-only commit.

Previously observed checkpoint `10d8880d8e6b94c8fc4024c8a42cd8f45b84439e` passed focused integration tests, the full repository race suite, vet and build in run `37102997599`. Final source verification runs separately in `37103276274`; its artifact records the actual committed candidate. This document does not presume a pending run has succeeded.

## Coverage added

Public protocol contracts, stable IDs/event sequences, Chat usage terminators, Responses failed/incomplete terminals, schema-validated function calls and results, official SDK response accumulation, explicit context branching, trusted-peer tenant boundaries, missing/deleted/expired response IDs, encrypted SQLite restart and wrong-key behavior, pagination without current-output leakage, non-idempotent retry restrictions, cancellation during setup/generation, body size/compression/short-write boundaries, malformed token usage, and the original workbench audit's >1 MiB request truncation regression.

## Deliberately unverified in CI

Real Prism transport/browser availability, model obedience to prompt-based tool/JSON adapters, real image usage, account/sandbox cloud-resource cleanup, sustained throughput, production network/CDN behavior, and the customized NewAPI wallet/precharge/settlement/refund chain. No live or paid model probe was performed. A green fixture suite is not a production SLA or an official-tokenizer-equivalence claim.

## Recovery/rollback boundary

This is an opt-in gateway configuration and an unmerged feature branch. Revert the branch commits or use the prior artifact to undo a future code rollout. Do not reuse gateway API keys as workbench/admin access. Do not reset the disconnected Windows worktree when it returns; reconcile its uncommitted changes with this branch first.
