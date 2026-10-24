# Polka handover

## Current work

Follow-up regression, fixed: refreshing during publication returned 504
because the only catalog connection was held by the writer. A dedicated WAL
reader keeps the previous catalog available; named update progress survives
failed list loads. Publication reuses existing bulk insert statements/caches
in the same transaction and indexes only new/restored entries. Delivered in
PR #14 (`bf47588`) together with search/cover performance fixes (`ed473ff`):
no duplicated ranked-search filtering, the library/source index lives in the
v6 schema, ANALYZE follows each publish, and covers revalidate by ETag
(book ID + per-library generation). The published cleanup image
`multiple-libraries-945f187` predates these fixes; do not rely on it for
update-heavy use. The live deployment is digest-pinned and untouched.

Persistent manual INPX/folder libraries, implemented by Luna agents as a stacked
PR series in the attached multiple-libraries worktree. The primary checkout has
unrelated user work; preserve it. No PR merge is authorized.

See `docs/specs/requests/multiple-libraries.md`, the compatibility contract and
work queue for requirements, reviewed commits and delivery. Core through minimal reader completion is published through PR #12
(`a6d31b6`). The follow-up cleanup shares update lifecycle handling, restores
saved legacy update hooks and removes unused OPDS scope support and duplicated
ISBN/JSON request paths. Optional OPDS work was parked outside the checkout
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

Publishing uses the existing cluster BuildKit via temporary localhost forwarding;
its public hostname does not resolve here. Zot HTTPS refused the builder
connection; the existing internal Zot service is reachable. Build only the
isolated worktree, publish a unique tag, and verify its registry manifest digest.
Pushing an image does not change the digest-pinned live deployment.

Cleanup validation passed full Go tests/vet, both binaries, 58 frontend tests
and production build. Disposable HTTP acceptance: `final-libraries-wrbdlpkm`;
unchanged-config native upgrade: `wp4b-native-9uh1r64z` (validation directory).
The legacy warning/rematch regression passed ten consecutive runs.


Cleanup image publication (no merge or deployment):
`registry.au.poloskov.net/ppoloskov/polka:multiple-libraries-945f187`
resolves to `sha256:c4514a6fbd02e4bc6eb09dfa51553aab72fa2f26bb06b8afc0a1c1583b423f2c`
for Linux/amd64, built from commit
`945f1877f1d7b081edec8ffc186f13d4d4aebc11`. Registry metadata matched the build;
the remote BuildKit smoke verified version, unprivileged `polka`, health 200,
creation of both databases, and graceful shutdown. Evidence:
`/private/tmp/polka-validation/cleanup-image/{build-metadata.json,registry-verification.json}`.
The latest tag and digest-pinned live deployment were unchanged.

Browsing image publication (no merge or deployment):
`registry.au.poloskov.net/ppoloskov/polka:multiple-libraries-browsing-2c29b9f`
resolves to `sha256:fe863d2125934de86b4749ed40ac5ea2e738c2652351d76830ca85a1e1aad63b`
for Linux/amd64, built from `codex/image-libraries-browsing` commit `2c29b9f`
(PR #12 stack + upstream PRs #16/#17/#18). Evidence:
`/private/tmp/polka-validation/browsing-image/build-metadata.json`.

Series-group image publication (no merge or deployment):
`registry.au.poloskov.net/ppoloskov/polka:multiple-libraries-browsing-48f397c`
resolves to `sha256:34c5dfbd8e5e6cd3231ebea9e63852a9727ea546be373c2c970e5dfc6e5ccdec`
for Linux/amd64, built from `codex/image-libraries-browsing` merge commit
`48f397c` (browsing stack + PR #14 perf, PR #16 cover caching, PR #17 INPX
series groups). Registry digest and config digest matched the build metadata.
Evidence: `/private/tmp/polka-validation/series-image/{build-metadata.json,registry-manifest.json,registry-headers.txt}`.
The digest-pinned live deployment was unchanged by publishing.

Home-perf image publication (no merge or deployment):
`registry.au.poloskov.net/ppoloskov/polka:multiple-libraries-browsing-a3fb6ba`
resolves to `sha256:c97e07bea40641eac661bf359e9a08241c7c16abe20b84915a71c6b86054efc8`
for Linux/amd64, built from `codex/image-libraries-browsing` merge commit
`a3fb6ba` (series-group stack + PR #18 home endpoint perf). Registry digest and
config digest matched the build metadata; smoke passed on the deployment node.
Evidence: `/private/tmp/polka-validation/home-perf-image/{build-metadata.json,registry-manifest.json,registry-headers.txt}`.
The digest-pinned live deployment was unchanged by publishing.

Publish-perf image publication (no merge or deployment):
`registry.au.poloskov.net/ppoloskov/polka:multiple-libraries-browsing-aac9850`
resolves to `sha256:74f79fec830065c067d4ad339ff0b6b9f308c63bb34d7aa9cfd07b5f72e2271f`
for Linux/amd64, built from `codex/image-libraries-browsing` commit `aac9850`
(home-perf stack + PR #19 INPX rekey fast path + PR #20 publish FTS rebuild and
quadratic probe fix). Registry digest matched the build metadata. Real-catalog
benchmark: the live-shaped consolidation publishes in 2m32s versus 4h15m+ for
the running live attempt. Evidence:
`/private/tmp/polka-validation/fts-rebuild-image-v2/{build-metadata.json,registry-manifest.json,registry-headers.txt}`.
The digest-pinned live deployment was unchanged by publishing.

One-time INPX consolidation completed 2026-10-23T05:26Z on the above digest:
`import done books=684168 duration=4m4.043s` and
`merged consolidated book references count=132103`. The same update had ground
for 4.5 hours without committing under the per-row delete + id-only probe
cleanup code. Post-checks: catalog committed (polka.db 1,069,318,144 bytes),
zero pod restarts, zero ERROR lines, home page ~100µs. Two earlier attempts
(old code, then v1 image without the probe fix) were rolled back by pod
restart; orphaned snapshot staging dirs were removed afterwards. The catalog
now carries inpx:v2 group keys throughout, so routine updates are exact-key
matches with no consolidation.

Reader experience delivery on PR #23 (`8bcdbc4`, branch `codex/reader-experience`,
based on #22):
- P0 typography: Literata, PT Serif and Noto Serif bundled locally
  (fontsource packages, cyrillic+latin subsets, no CDN), justification,
  language-tagged automatic hyphenation (read-meta carries the book
  language and the reader container sets `lang`), first-line indent and a
  paragraph-spacing scale; all persist through the reader-prefs API.
- P1 navigation: in-book search (`GET /api/v1/read/{id}/search`, chapter
  matches with counts and snippets, in-chapter `<mark>` highlighting on
  jump), reading bookmarks in users.db (CRUD at
  `/api/v1/read/{id}/bookmarks`, same-spot saves update, localStorage
  fallback in public mode) with a 26ab button and a panel tab, and a seekable
  progress bar weighted by per-chapter rendered sizes (read-meta also
  carries chapter sizes).
- Sync-mode desktop readers keep their existing routes; the new endpoints
  are server-mode only.
- Review round 4 (commit `f499986` / `45faed8` on the PR branch): PDF
  hook-order crash fixed (memos moved above the early returns), search
  snippets clamp context bounds (beginning/end matches), saveProgress
  propagates failures (banner works on plain saves), per-library genre
  counts refresh on publication and disabled-library deletions, FB2
  position capture/restore walk code-point segments across search `<mark>`
  fragments, bookmarks joined reference preservation (MaxBookID floor +
  consolidation transfer) and store real FB2 locators, and the web author
  page adopted `load more` (limit/offset + hasMore/nextOffset) — closing
  the author-pagination performance item.

Session-discipline incident (recorded per the review trust contract):
several shell commands ran without an explicit workdir and executed in the
PRIMARY checkout (`/Users/paul/projects/polka`), receiving stray writes
(prettier rewraps of fetchBooks/reader/ReaderPage, round-3 i18n keys,
fontsource deps in web/package.json + lock, one reader.js append) followed
by an erroneous multi-file `git checkout` revert. Recovery: reverted files
were reconstructed to their as-found state (ReaderPage.jsx/i18n/reader.js/
fetchBooks restored, SearchPage.jsx user list-view feature rebuilt from the
captured diff, package files restored via npm install); the primary's own
web test suite passes (34 tests / 8 files) and its dist rebuilds. Residual:
the primary dist content hash differs from its pre-incident build
(B4HNniqO vs C4L1KZe8) without an identified content difference 2014 all user
features verified present (BookCard author links, BookListPage list-view
and grouping, SearchPage views); the as-found bundle bytes were lost to a
dist rebuild before comparison. RULE: every command in cross-repo sessions
must set workdir explicitly; never rely on sticky cwd.
