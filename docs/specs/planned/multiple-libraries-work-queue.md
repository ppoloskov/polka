# Multiple libraries: reviewable PR series

User authorized implementation and PR publication on 2026-10-22. Target is
`ppoloskov/polka`; PRs stack on preceding reviewed branches, with no merges.
Luna agents write tests first, implement bounded work, and report evidence;
architect reviews scope/style/behavior before Git publication.

## Work packages

| Package | Behavior and scope | Dependencies | Required validation | Status |
| --- | --- | --- | --- | --- |
| WP1 | Automatic additive default-library migration, registry, durable allocator shared by existing insert paths, reserve known auth refs before startup/import; newer-schema rejection. No source scans or changed public routes. | None | v5/v1/fresh/reopen, row/relation/deletion preservation, Clear/reopen/import/upload allocation | Exact commit validated; publishing |
| WP2 | Resolve each book's owning root across all serving paths while preserving dynamic legacy root and uploads | WP1 | same config/root changes, two roots with same locator, reader/download/cover/export/email, existing suites | Reviewed; HTTP/server/library tests pass; exact-commit verification next |
| WP3 | Disk-backed snapshot staging and atomic target-only publication; missing separate from admin deleted; preserve personal state and other libraries | WP1 | diff/idempotence/remove/reappear/admin hidden, rollback/error/cancel/restart, no global search/index clearing | Reviewed; focused tests/vet pass; exact-commit validation next |
| WP3b | Availability filtering before exposing update entrypoints; missing/disabled excluded from normal counts, browsing, files and cached chapters | WP2, WP3 | missing/disabled counts/search/author/series/file/recommendations, reader-cache removal and restore | Test writing |
| WP4 | INPX saved-source updater and existing CLI/web import adapters over same engine | WP2, WP3 | strict parse/completeness/key policy, source path/upload/default target, failure/retry/empty approval, unchanged old contracts | Pending |
| WP5 | Recursive folder adapter, full-byte hashes, existing metadata parsers/fallback | WP2, WP3 | recursive add/change/remove, duplicate copies, symlinks/traversal/unreadable/unstable inputs, realistic source tree | Pending |
| WP6 | Admin libraries UI/API/CLI, combined browsing/filter/source badges and unavailable personal entries | WP4, WP5 | admin authorization/demo/connected proxy, counts/pagination/filter/bulk, existing UI tests/build and real HTTP workflows | Pending |
| WP7 | Upgrade/restart and end-to-end acceptance, final compatibility cleanup/docs | All | previous-version data and unchanged config/clients; both source types; source failure; repeated updates; full Go/Node/build/vet; actual container proof where available | Pending |

Each package changes only its owned modules and necessary documentation. No
unrelated cleanup, new abstractions, generated assets, local diagnostic files,
dependency version changes, or inherited reader protocol changes. If a package
exceeds a reviewable diff, split it at a tested behavior boundary before delivery.
Every PR must pass its surrounding tests and preserve old workflow operation;
all feature claims wait for WP7 operational acceptance.

## Traceability and failure boundaries

| Source requirement | Work coverage | Test/evidence | Status |
| --- | --- | --- | --- |
| Image-only upgrade; no setup/client change | WP1, WP2, WP4, WP7 | v1/v5 upgrade, unchanged mounts/flags, old endpoints and state | Pending |
| Separate copies, no clashes | WP1, WP3–WP6 | two libraries same source ID/path, distinct public IDs/state | Pending |
| Manual INPX and folder updates | WP3–WP6 | admin/CLI first-run, repeat, durable diff and restart | Pending |
| Failure leaves published catalog intact | WP3–WP5, WP7 | malformed/partial/missing/full-disk/cancel/commit fault tests | Pending |
| Removed entries retain personal state | WP3, WP6, WP7 | separate users DB, unavailable list/history cards | Pending |
| Per-root serving and existing source formats | WP2, WP4, WP5, WP7 | ZIP/7z/plain folder reader/cover/download/export/mail | Pending |
| Minimal original style and slim PRs | Every WP | architect diff review; existing helper reuse; per-PR checks | Required |

Rollback: schema changes are additive; do not promise old binaries support a
new schema. Operational downgrade uses a coherent pre-upgrade backup. Newer
binaries reject unknown future schemas. Missing source data never triggers
destructive fallback. No physical file deletion or automatic identity repair.

## Mock PR annotations

- reviewer-blocker: any forced setup, config/mount/client change or lost numeric
  reference on upgrade blocks the series, even if new-feature unit tests pass.
- reviewer-test: source adapters must prove real entrypoints and durable repeat/
  failure behavior. Unit-only evidence cannot certify operational acceptance.
- reviewer-risk: unknown old IDs cannot be repaired from current rows; forward
  safety must include every writer and known persisted personal reference.

## Execution record

Baseline Go tests passed for store/importer/library/server/config in the primary
checkout with existing dependency versions. Revalidate isolated fork baseline.
No Docker available locally; actual image acceptance is not yet demonstrated.

WP1 red tests failed on missing registry/reservation APIs, then passed after
implementation. Focused store/importer/auth/CLI tests and desktop compile pass;
store vet passes. Review removed generic schema introspection and custom UUID
formatting in favor of ordinary migration SQL and stdlib opaque IDs. Corrected
the old synthetic v4 fixture to remove later-version columns before migration;
all original series assertions remain. Command regression proves reimport IDs
exceed orphan user progress refs. Exact commit/full upgrade checks follow.
Frontend baseline: unchanged lockfile install/build pass; Node 26 needs
`NODE_OPTIONS=--no-experimental-webstorage` for all 34 tests to pass. No dependency
or frontend source changes are needed for that environment difference.

WP1 exact commit `3b8006c` passed full Go tests, full vet, and server/desktop
builds from an isolated archive. Native old-binary -> candidate upgrade retained
session/login, catalog ID/content, rating, list membership, normal/orphan progress.
Post-upgrade upload allocated ID101 above orphan ID100 and survived restart;
one legacy registry entry remained. Positional CLI replacement also reserved
the orphan reference. Evidence: `/private/tmp/polka-validation/wp1-upgrade-6z3udpof`.
This proves native data/config compatibility; Docker proof remains outstanding.

WP2 HTTP regression failed on the old global-root 404, then passed after per-book
resolution. Existing server/library suites pass. Review preserved export's
skip-missing behavior, unset legacy-root progress saves, and original field
formatting. No query positional scan or reader protocol changed.

Status: implementation authorized; WP1 publishing, WP2 reviewed, WP3 implementing.
Next: exact-commit WP2 validation/publication, then snapshot availability work.

Published: [foundation PR #1](https://github.com/ppoloskov/polka/pull/1), then
[roots PR #2](https://github.com/ppoloskov/polka/pull/2) stacked on #1. Both exact
commits passed full Go tests/vet/build and native old-binary upgrade checks.

WP3 implements only internal staging/publication, not a new public import flow.
Red tests exposed missing APIs; green snapshot tests cover two namespaces,
idempotence, remove/reappear, source/admin deletion, duplicate poisoning, empty/
cancelled input, and mid-insert rollback of rows/search/allocation floor. Review
removed mutable-record comparison machinery and redundant relation helpers;
one existing insert path is reused inside the publication transaction. Full
store validation will use the committed tree without pending availability tests.
