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
state, preventing ID reuse after Clear/reopen. Exact commit `3b8006c` passed all
Go tests/vet and both builds from an archive. Native upgrade kept login/session,
IDs/content, lists/ratings/progress; upload ID101 avoided orphan reference100 and
survived restart with one legacy entry. Positional CLI import --replace passed.
Artifacts: `/private/tmp/polka-validation/wp1-upgrade-6z3udpof`. Foundation PR
publication is underway. No source scans start on upgrade.

WP2 per-book roots implemented/reviewed: HTTP test exercises the same locator in
two roots with no global root, download/export/TXT reader and legacy root changes
across starts. Server/library suites pass. Existing upload default, export
skip-missing and unset-root progress behavior retained. Exact-commit validation
and publication next. WP3 disk-backed atomic snapshot tests reviewed;
implementation underway, not yet exposed through CLI/API or called verified.

Published PRs: https://github.com/ppoloskov/polka/pull/1 (foundation) and
https://github.com/ppoloskov/polka/pull/2 (roots, stacked on foundation).
WP2 exact `fde13fa` passed full tests/vet/build and native upgrade with unchanged
legacy root config. WP3 snapshot primitive is now reviewed; focused tests/vet
pass. It uses immutable keys and disk-backed staging, preserving target IDs,
admin deletion and other libraries, with rollback of all catalog/search/allocator
changes on error. Exact-commit checks and publication are next. Availability
tests are being implemented separately; exclude them from the snapshot commit.

Snapshot PR #3 published: https://github.com/ppoloskov/polka/pull/3, exact
`2a678ef`, all Go tests/vet/both builds pass. Availability work is reviewed and
store/library/server tests pass; excludes missing/disabled books from counts,
recommendations, files and search pages, and checks DB before reader cache hits.
Admin restore keeps missing books out of FTS. Zero-book author/series dictionary
listings retain legacy behavior. Exact-commit validation/publication next.
INPX staging is reviewed separately; legacy adapter and folder tests are underway.

Availability exact `1e7616b` passed full Go tests/vet/both builds. INPX source
staging ready for its own commit: strict structural completeness on an owned copy,
exact immutable `inpx:v1:` record keys, deletion excluded from key, duplicate
poisoning, malformed/corrupt input rejection, and golden-key compatibility test.
Metadata corrections conservatively remove/add; unchanged index records are not
a claim of unchanged archive bytes. Added source-key index at first publication:
20k temporary scale probe 1.86s initial, 3.77s repeat; query plan uses the index.
Legacy adapter tests confirm destructive replace still needs fixing before public
updates are accepted. Folder implementation is separate and uncommitted.

Availability PR #4: https://github.com/ppoloskov/polka/pull/4. Source staging
exact `254d42f` passed all Go tests/vet/both builds. The independent folder adapter
is ready to commit before the legacy import wiring: os.Root confines traversal
and opening, full hash and metadata share a descriptor, EPUB ReaderAt wrapper
avoids reopening paths, actual extension case is retained, and added dates/hash
fields are populated. Focused importer/library tests/vet pass. The legacy adapter
worker is fixing provenance, portable source retention, and cache invalidation
before its commit; exclude those paths from the folder commit.

Known legacy limit: lost/reused historical IDs and IDs retained only by offline
clients cannot be reconstructed. Guarantee forward allocation safety from the
known persisted migration boundary; do not expand sync protocols to claim repair.

Current delivery: PRs #1–#5 are open and unmerged. Folder commit `4581178`
passed full Go tests/vet/both builds from an isolated archive; PR #6 is being
published. Legacy import adapters are reviewed and ready for a separate commit
and native upgrade verification. They preserve the DB on failure, retain verified
uploaded indexes, commit source provenance atomically, and reset cover caches.
Explicit successful replacement conservatively retires unknown legacy catalog
rows without reassigning their personal references; hashed uploads remain.
Management API work is pending in separate files; never stage it with adapters.
