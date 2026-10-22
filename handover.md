# Handover

## Multiple libraries — 2026-10-22

User authorized Luna implementation agents and a slim validated PR series.
Base: fork main `52a2e29`; primary checkout contains unrelated dirty work and an
unpushed identity experiment, so implementation uses this isolated worktree.
Do not import those changes into this series.

Confirmed: separate entries/state across libraries, manual updates only, image-only
upgrades with unchanged existing workflows. Preserve numeric IDs and legacy root
flag/env resolution. Migration wraps rows without source probing or reimport.

Baseline store/importer/library/server/config Go tests passed using `nodynamic`.
Shared validation caches: `/private/tmp/polka-validation/{gomod,gopath,gocache}`
for GOMODCACHE, GOPATH and GOCACHE. Docker is unavailable locally; do not claim
container/image-swap acceptance from native tests. Frontend dependency setup is
complete using the unchanged lockfile; build passes and all 34 tests pass with
Node26 `NODE_OPTIONS=--no-experimental-webstorage`. See work queue for batch status.

WP1 implemented/reviewed: additive v6 migration, default legacy binding, opaque
registry IDs, durable allocation floor shared by imports/uploads, auth max-ID
query and startup/CLI reservation. Focused store/importer/auth/CLI tests and
desktop compile pass; store vet passes. Known refs include missing-book personal
state, preventing ID reuse after Clear/reopen. Exact-commit upgrade verification
and PR publication are next. WP2 serving tests reproduced the global-root 404
and its implementation is underway separately. No source scans start on upgrade.

Known legacy limit: lost/reused historical IDs and IDs retained only by offline
clients cannot be reconstructed. Guarantee forward allocation safety from the
known persisted migration boundary; do not expand sync protocols to claim repair.
