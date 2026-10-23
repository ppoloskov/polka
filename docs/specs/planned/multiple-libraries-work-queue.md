# Multiple libraries: reviewable PR series

User authorized Luna implementation and publication on 2026-10-22, then requested
consolidation and minimal completion. Target: `ppoloskov/polka`; no merges.
Each branch stacks on the previous reviewed commit. Preserve unrelated primary
checkout work; implementation lives in the attached multiple-libraries worktree.

| Package | Behavior | Delivery / validation |
| --- | --- | --- |
| Foundation | Additive migration, legacy binding, globally reserved IDs | PR #1, `3b8006c`; full Go checks and native upgrade |
| Roots | Serve every book from its owning root | PR #2, `fde13fa`; full Go checks and native upgrade |
| Snapshots | Disk-backed staging and atomic target-only publication | PR #3, `2a678ef`; full Go checks, rollback/repeat tests |
| Availability | Missing/disabled excluded from browsing/files/cache hits | PR #4, `1e7616b`; full Go checks |
| INPX | Strict complete staging and exact immutable keys | PR #8, `254d42f`; full Go checks, golden-key and 20k scale probe |
| Folders | Recursive confined reads and full content hashes | PR #6, `4581178`; full Go checks |
| Legacy adapters | Shared updater; no destructive replacement | PR #7, `e812276`; full Go checks and native upgrade/replace |
| Management API | Register/name/enable/manual-update; diff status | PR #9, `e8d15a9`; full Go checks and HTTP lifecycle |
| Catalog scope | Filter before pagination; source fields; personal retention | PR #10, `9ed3655`; full Go checks in pr10 |
| Admin UI | Existing screen controls both sources and manual updates | PR #11, `eca0ab3`; 41 web tests/build and both binaries in pr11 |
| Minimal completion | Reader selector/badges/unavailable cards; shared metadata parser | PR #12, `a6d31b6`; exact-head Go/web checks, native upgrade and HTTP acceptance |

Full Go checks mean `go test ./...`, `go vet ./...`, server and desktop builds
from each exact committed archive. Frontend checks use the unchanged lockfile;
Node26 requires `NODE_OPTIONS=--no-experimental-webstorage`. GitHub check runs
were absent on published heads; local evidence is not a GitHub CI result.

## Required final gate

- Both source types: register without scan, update/repeat, add/change/remove,
  unreadable/corrupt/empty failure, restart and durable diff/state behavior.
- Same source IDs/locators across libraries: different public IDs, correct roots,
  independent Polka progress, visible source names and unavailable personal cards.
- Catalog filters/counts/pagination and bulk downloads use the visible library.
- Old flags/env/mounts, login/session, import/upload, reader/download, lists,
  ratings/progress and known orphan refs survive unchanged-config native upgrade.
- Full Go/web checks and rendered UI review. Docker is absent locally; native
  executable/data acceptance must not be described as an actual image swap.

## Scope and gotchas

No watchers, schedules, new dependencies/services, per-library DBs, generic jobs,
source deletion, fuzzy matching, new management CLI or reader protocol changes.
The optional OPDS filter patch is deferred outside the checkout under
`/private/tmp/polka-validation/deferred-opds`; existing combined OPDS remains.
The old bulk importer has only test-fixture callers; leave it rather than rewrite
fixtures in this feature. Uploads and folder scans share metadata parsing.

Unknown legacy identities are never remapped. Folder moves/byte changes and
INPX record corrections conservatively create new entries. Known persisted
numeric refs seed the allocation floor; historical lost/offline-only refs cannot
be reconstructed. KOReader's digest-only bridge cannot distinguish identical
copies; Polka's numeric-ID progress is independently tested.

PR #5 was closed after a branch-name reuse; #8 is its exact validated replacement.
Always audit head/base refs before pushing. No force pushes or merges.

## Final consolidation evidence

The optional OPDS patch was parked, duplicate upload parsing was removed (48
lines), and chronological handover/queue repetition was condensed. No fixture
engine rewrite, new CLI, dependency, watcher or protocol change was added.
Upload duplicate checks now stay within the legacy target, with HTTP coverage
and a 55-foreign-candidate scope-before-limit regression. Byte-identical FB2
copies have a real reader/progress regression proving separate Polka state.
A four-line Windows guard skips unsupported directory sync after retained source
files are synced; importer Windows cross-compilation passed, runtime untested.

Frozen preview passed all Go tests/vet/both builds and 55 web tests/build.
Native final upgrade: `/private/tmp/polka-validation/wp4b-native-vc4938zt`.
Both-source HTTP acceptance: `/private/tmp/polka-validation/final-libraries-hophb25q`.
Covered update/repeat/change/removal, corrupt/missing/empty source rollback,
read-only source hashes, namespace/root isolation, personal retention, scoped
paging/bulk export, enable/disable and durable restart. Live IAB review confirmed
manual update counts, source selection, unavailable cards and own-root reading;
management wording/status placement corrections followed; the updated web suite
passes 57 tests/build. Exact committed-head
checks and PR metadata audit passed for PR #12. Docker is unavailable locally;
follow-up image publication uses the existing remote BuildKit and Zot services.
