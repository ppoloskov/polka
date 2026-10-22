# Polka handover

## Current work

Persistent manual INPX/folder libraries, implemented by Luna agents as a stacked
PR series in the attached multiple-libraries worktree. The primary checkout has
unrelated user work; preserve it. No PR merge is authorized.

See `docs/specs/requests/multiple-libraries.md`, the compatibility contract and
work queue for requirements, reviewed commits and delivery. Core, catalog scope and admin UI are published through PR #11. Consolidated
reader UI, metadata reuse and scoped upload deduplication are implemented; final
exact-head checks gate publication. Optional OPDS work was parked outside the checkout
at `/private/tmp/polka-validation/deferred-opds`; do not reintroduce it.

## Boundaries

- One catalog, separate users DB. Public IDs are allocated globally, never from
  provider IDs; seed the floor from known user refs before startup/import.
- Migration creates one legacy binding, preserves IDs and uses the original
  dynamic library-dir config. No startup source probing, reimport or new setup.
- Snapshot stages on disk, then publishes one target transaction. Missing is
  separate from admin deletion. Unreadable/partial/empty input cannot wipe data.
- No fuzzy identity repair. Changed folder path/bytes or INPX exact record gets
  a new entry; old personal references remain unavailable. Hashed uploads stay.
- Paths/kinds are fixed after registration. Public metadata excludes server
  paths. Clear owner cover caches and genre counts after successful changes.
- Filter before LIMIT/OFFSET, including personal shelves/collections; positional
  Book scans must stay aligned. Ordinary reads remain availability-gated.
- KOReader's digest bridge is ambiguous for identical copies. Ordinary Polka
  progress remains per-book ID; no reader protocol change belongs here.

## Verified evidence and environment

Exact archives and warm Go caches: `/private/tmp/polka-validation`. Go uses
`GOFLAGS=-tags=nodynamic`; cache variables point to gomod/gopath/gocache there.
Full Go tests/vet/both binaries passed through catalog pr10. Admin pr11 passed
41 web tests/build and both binaries with its own generated frontend.
Node26 tests need `NODE_OPTIONS=--no-experimental-webstorage`; no dependency
changes. Existing Vite mixed-import chunk warnings persist.

Native baseline-to-candidate upgrade evidence:
`/private/tmp/polka-validation/wp4b-native-bmorgdwb` and native_upgrade_wp4b.py.
Same paths/config preserved sessions, ID1/content, ratings/lists/progress and
orphan100. Upload101 survived restart. Corrupt replacement preserved catalog and
provenance; explicit replacement retained oldID1 unavailable, allocated102 and
remained stable on repeat/restart. Docker is unavailable; no image-swap claim.

Localhost tests and worktree writes may need sandbox escalation. GitHub CLI auth
is invalid; Git agent has a secret-safe Git credential/REST workflow. Never print
credentials. Verify unique branch names and exact PR head/base before pushing;
PR #5 was superseded by #8 after branch reuse. Attach every PR; never merge.

Final preview passed full Go checks, both builds and 55 web tests/build. Native
upgrade rerun: wp4b-native-vc4938zt; multi-source HTTP acceptance:
final-libraries-hophb25q (under the validation directory). Live IAB review covered
manual update counts, source filter, missing personal cards and own-root reader.
Windows uploaded-source directory sync is skipped after syncing the file; importer
cross-compile passed, Windows runtime untested. Legacy upload dedup stays within
its target, so named-library copies cannot block old upload workflows.

Final management QA fixes passed 57 web tests/build: default-root warning is
explicitly legacy-scoped, and named update status no longer appears in the old
INPX import section. Status responses without libraryId remain compatible.
