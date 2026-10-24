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

Cover and reader-image 404s after the consolidation were diagnosed 2026-10-23
and are NOT a consolidation regression: the probed IDs are all live winners
with current paths, archives on disk are .7z (the .zip→.7z fallback works),
and book files open fine. The source is the dump layout itself: FB2 files in
this Flibusta dump have `<binary>` payloads stripped — they reference
`#cover`, `#0`, `#1`… that are absent from the book. The payloads live in
optional sidecar trees next to the book archives: `covers/<archive>.zip|.7z`
(covers by book id, already served) and `images/<archive>.zip|.7z`
(illustrations as `<bookid>/<imageid>`, previously never consulted — every
inline illustration 404'd while reading). Some books (e.g. journal pieces
like fb2-161831-166042.zip/163747) have no cover anywhere; their cover 404
is truthful.

New-format fix delivered in PR #21 (`1f8200b`, based on #20): `Library.Binary`
falls back to the images sidecar (exact `<bookid>/<imageid>` lookup, ZIP/7z,
content-sniffed MIME, JPEG XL normalized like other sidecar images) and serves
the conventional `cover` id from the covers sidecar; web reader hides chapter
images that fail to load so unresolved references stop showing broken-image
placeholders; README documents the sidecar layout. Verified in-pod against the
real dump: `Binary("0")` → 59 KB jpeg, `Binary("1")` → 1.7 KB jpeg,
`Binary("cover")` → 294 KB jpeg (all 404 before the fix).

Review follow-ups (2026-10-23, on the stacked series):

P1, delivered in PR #21 (`f0c2ce6`): consolidation loser→winner pairs are
now recorded in a `pending_book_merges` table inside the publication
transaction (table created lazily at store open, deliberately without a
schema-version bump so a rolled-back older binary runs unaffected). The
users-db merge runs from the persisted pairs after commit and clears them;
failure keeps the pairs, and startup, the next publish and the CLI import
replay them idempotently through the existing `auth.MergeBookRefs`. Insertion
order preserves publication order so chains replay winner-of-pairs-first.

P2, delivered in PR #21 (`3b41bee`): re-inserted FB2 binaries are emitted
through encoding/xml (ids escaped, never truncated to the old 200 chars),
with a round-trip test using an id containing quotes and ampersands.

P2, delivered by rewriting PR #16 at its source (branch rebased #17–#21 on
top): the shared in-memory failed-cover blacklist was removed — it
permanently suppressed covers per session, including on transient network
errors, and nothing invalidated it after imports. Request suppression now
relies on the server's `Cache-Control: private, max-age=3600` missing-cover
responses plus the pre-existing local component fallback. Web tests dropped
71→67 accordingly. The digest `multiple-libraries-browsing-1f8200b` and
`...-7c6552a` images below predate this rewrite; the newest image entry
supersedes them.

P2, disposition only (no code change): the identity experiment in the
PRIMARY checkout `/Users/paul/projects/polka` (fuzzy author/title
normalization, ID allocation above the current maximum, clearing the active
catalog mid-replacement) remains excluded from delivery — the isolated
worktree owns the feature. Preserving the dirty primary checkout is
intentional per the working conventions; it is not a production alternative
and must never be staged or published with the feature.

Review image publication (no merge or deployment):
`registry.au.poloskov.net/ppoloskov/polka:multiple-libraries-browsing-a374461`
resolves to `sha256:53d6305e93cee4e46c716f3b0fc98066c7116efad9f1f1785b9a2fd0893afd6b`
for Linux/amd64, built from `codex/image-libraries-browsing` commit `a374461`
(full sidecar format support + PR #21 durable merge recovery + encoding/xml
binary emission + the rewritten PR #16 HTTP-cache-only cover behavior).
Registry digest matched the build metadata. Evidence:
`/private/tmp/polka-validation/review-image/{build-metadata.json,registry-manifest.json,registry-headers.txt}`.
Superseded by the ordered-merge image below: review follow-up found that the
server and CLI replay converted the ordered pending pairs into a map, letting
Go's map iteration reverse a consolidation chain (a winner retired as a later
publication's loser) and strand state on the intermediate winner while
acknowledging the pairs. Delivered in PR #21 (`d462cd0`): `auth.MergeBookRefs`
now takes the ordered pair slice (`auth.BookRefPair`) and applies pairs in
publication order, with a 20-round chained replay regression test.

Page-latency delivery (target: home, book-list and book-detail pages under
1s at p95 against the real catalog), delivered in PR #22 (`f579688`, based
on #21). Measured end-to-end on a copy of the live 1.6M-row catalog
(664k visible; a disabled legacy library interleaved with enabled ones;
authenticated bench user with seeded reading progress):

- Maintained counters: publication records `visible_book_count` and
  per-genre counts in meta inside the publication transaction; direct
  mutations recompute eagerly. The inline 1.4s visible-count scan no longer
  rides user requests (measured 1.7s p95 on getConfig before).
- Partial live indexes (added/rate over deleted=0, missing=0) remove
  availability row fetches from shelf walks.
- Per-genre visible member lists: built once per genre, paginated in memory,
  dropped on every visibility change (deletion, restore, publish sweep —
  regression-tested). A 107k-member genre paid 92-180ms per page before.
- Reader pool 1 → 8 WAL readers: one reader connection serialized all reads
  behind the slowest query (genre recommendations measured 10.7s at 8-way
  concurrency). In-process read parallelism remains capped by the pure-Go
  driver's process-wide serialization (~1.2x measured); warm pages are now
  cheap enough that p95 holds: genre recs 10.7s → 0.39s, home 0.36s → 0.07s
  at c=8; pages hold p95 46ms during concurrent admin writes.

Measured edges (documented in the PR): deep-offset pagination of the
built-in newest shelf (~page 1000) ~270ms single / ~2.8s at 8-way
concurrency; the first request per genre builds its member list (~200ms for
the largest); an upgraded pre-count database computes totals once (~7s) on
the first visibility read and records them. A literal enabled-library
IN-list replacing the availability EXISTS was tried and reverted (planner
regression: 2.2s deep page); the note stays in code.

Benchmark harness (disposable, not committed): /private/tmp/polka-perf —
server binary + DB copies + `drive.mjs` (p50/p95/max per endpoint,
concurrency), endpoint lists in eps*.txt. Rebuild: `NODE_OPTIONS=
--no-experimental-webstorage npm run build` in web/ then target build with
-tags nodynamic; note `go:embed all:dist` needs web/dist present.

Page-latency image publication (no merge or deployment):
`registry.au.poloskov.net/ppoloskov/polka:multiple-libraries-browsing-448e7ae`
resolves to `sha256:7f96a1d2b2021e32658e9b93437731d562178f93e200377927e899795015eda5`
for Linux/amd64, built from `codex/image-libraries-browsing` commit `448e7ae`
(the full browsing stack through #22 with maintained counters, partial live
indexes, per-genre member lists, reader pool 8). Registry digest matched the
build metadata. Evidence:
`/private/tmp/polka-perf-image/{build-metadata.json,registry-manifest.json,registry-headers.txt}`.
SUPERSEDED by the review-fix image below; do not deploy 448e7ae.

Review-follow-up delivery on PR #22 (`937ace2`, image branch `e237644`) —
all six review items addressed: upload/delete/restore/library-toggle/clear
now maintain counts in their own transaction (deltas) instead of
committing first and scanning later (the upload failure could previously
delete the file of a committed book); genre pagination guards past-list
offsets and returns exactly its requested row count; the per-genre cache
install is guarded by a generation counter against publication races;
missing totals initialize transactionally (EnsureCountsInitialized) with a
background seed at Open (Close waits for it); genre recommendations sample
member ids uniformly (partial Fisher-Yates) instead of a contiguous
window; the stray debug print is removed and pagination reuses BooksByIDs.
Reader page cache reduced to 16 MiB per connection (writer keeps 64 MiB):
measured RSS 412-423 MB on the 1.6M-row benchmark under load. Re-measured
after fixes at c=8 p95: home shelves 66ms, genre pages 8ms, catalog shelves
0.6s, covers ~1ms; deep-offset edge and one-time initialization unchanged
in shape. Evidence of the process failure worth remembering: two publishes
of image2 shipped the SAME digest as 448e7ae because the retry script kept
the previous image's directory paths; the fix is to rewrite the whole
script (paths AND tag) per build and verify the digest differs from the
previous build before recording it.

Review round 3 delivery on PR #22 (`4deea61`, image branch `ce991fb`):
- Reader save failures are surfaced (non-blocking banner, cleared on the
  next successful save; plain saves report too) — network/other server
  errors were silent before.
- The search page aborts superseded requests (AbortController threaded
  through the api layer) instead of only ignoring responses.
- Scoped genre search serves maintained per-library counts
  (`library.<id>.genre_book_counts`, recorded by publication, seeded for
  every library at initialization, backfilled transactionally on first
  read; unavailable libraries get no scoped list, matching the direct
  scan). The scoped list was a direct GROUP-BY scan per request (1-2s on
  the 1.6M-row library) — now ~2ms.
- Author pages are paginated server-side (`AuthorBooksPage`; the endpoint
  accepts `limit`/`offset`, default full list for compatibility). The
  largest author measured 1,942 visible books: full page 59-129ms, a
  60-card page at offset 900 46ms. Reader digest drain and book-details
  lazy metadata writes remain documented profiling candidates.
- `SimilarBooks` returns ordinary card rows via `BooksByIDs` (structured
  author references for author-page links).
- Catalog-genre sample shelves display by title as documented.

Acceptance evidence is endpoint-level (real server binary, real pages,
authenticated user, concurrency, during-write measurements). Browser
readiness is approximated by the home API pattern (config + shelves +
covers concurrently: p95 341ms for the pattern); cover extraction is not
part of the local benchmark (no archives in the harness root; real-cover
serving was verified in-pod). Measured outstanding edges: concurrent
deep-offset pages (~2.7s at 8-way, page ~1000) and the one-time
initialization (~2-7s in the first-serve window on an upgraded database).
Windows runtime acceptance remains outstanding (importer cross-compile
verified earlier; runtime untested).

Round-3 image publication (no merge or deployment):
`registry.au.poloskov.net/ppoloskov/polka:multiple-libraries-browsing-ce991fb`
resolves to `sha256:f257b916c65e47528a97fddcdf54d379915e94225626a23e1cd98ca9525948b5`
for Linux/amd64, built from `codex/image-libraries-browsing` commit `ce991fb`
(all review follow-ups through round 3). Registry digest matched the build
metadata and differs from all previous page-latency digests. Evidence:
`/private/tmp/polka-perf-image4/{build-metadata.json,registry-manifest.json,registry-headers.txt}`.
The digest-pinned live deployment was unchanged by publishing; this tag
supersedes all earlier browsing tags for deployment.

Review-fix image publication (no merge or deployment):
`registry.au.poloskov.net/ppoloskov/polka:multiple-libraries-browsing-e237644`
resolves to `sha256:427655cd33cf9ee916d0a927730e2faac6d5f24f6cd8d83682caff929275d643`
for Linux/amd64, built from `codex/image-libraries-browsing` commit `e237644`
(all review follow-ups). Registry digest matched the build metadata and
DIFFERS from the superseded 448e7ae digest. Evidence:
`/private/tmp/polka-perf-image2/{build-metadata.json,registry-manifest.json,registry-headers.txt}`.
SUPERSEDED by the round-2 image below. The round-2 review probes found two
remaining upload-delta bugs: uploads into disabled libraries bumped genre
totals while the book is invisible, and duplicate genre codes
(`["sf","sf"]`) doubled the delta although only one membership is stored.
Fixed in PR #22 (`a0b2ff9`): genre deltas apply under the same
enabled-library condition as the visible-count delta and count each code
once; covered by `TestBookCountDeltasMatchVisibility`, which also
cross-checks the maintained totals against an exact recount.

Round-2 image publication (no merge or deployment):
`registry.au.poloskov.net/ppoloskov/polka:multiple-libraries-browsing-7f86cce`
resolves to `sha256:a3950d398552a364b7e15ca5a65b160feb577bbf362ca81bd79c046a16c94782`
for Linux/amd64, built from `codex/image-libraries-browsing` commit `7f86cce`
(all review follow-ups including round 2). Registry digest matched the build
metadata and differs from both earlier page-latency digests. Evidence:
`/private/tmp/polka-perf-image3/{build-metadata.json,registry-manifest.json,registry-headers.txt}`.
The digest-pinned live deployment was unchanged by publishing; this tag
supersedes all earlier browsing tags for deployment.

Ordered-merge image publication (no merge or deployment):
`registry.au.poloskov.net/ppoloskov/polka:multiple-libraries-browsing-e83c6a1`
resolves to `sha256:4284c466462222c1d1e916f80a9d694b1ffde80b021c27fbf917395206349854`
for Linux/amd64, built from `codex/image-libraries-browsing` commit `e83c6a1`
(all review follow-ups above + ordered merge replay). Registry digest matched
the build metadata. Evidence:
`/private/tmp/polka-validation/ordered-merge-image/{build-metadata.json,registry-manifest.json,registry-headers.txt}`.
The digest-pinned live deployment was unchanged by publishing.

Sidecar-format image publication (no merge or deployment):
`registry.au.poloskov.net/ppoloskov/polka:multiple-libraries-browsing-1f8200b`
resolves to `sha256:65b6b1838427bb2b411875285f6aa0ebeb2c49a092cd87c1f78fac3767dd37b1`
for Linux/amd64, built from `codex/image-libraries-browsing` commit `1f8200b`
(browsing stack + PR #21 sidecar images: covers/, images/ trees for
coverless/stripped Flibusta dumps). Registry digest matched the build metadata.
Evidence: `/private/tmp/polka-validation/sidecar-image/{build-metadata.json,registry-manifest.json,registry-headers.txt}`.
The digest-pinned live deployment was unchanged by publishing.

Download re-embedding delivered on PR #21 (`f3d2eb4`): `Library.CompleteFB2`
scans referenced-vs-embedded FB2 ids, collects missing payloads from the
images/ sidecar in one pass per archive (plus the `cover` id from covers/)
and re-embeds them as `<binary>` elements; served from the FB2 attachment,
`fb2.zip`/OPDS, bulk export and e-mail. The reader path and `.compact.fb2`
intentionally stay on the original file; unchanged books keep exact bytes.
The zip/7z sidecar scan was consolidated into one shared helper. Verified
in-pod against the real dump, book 847392: 699,936 → 952,981 bytes with
`cover`/`0`/`1` base64 payloads embedded and a single closing
`</FictionBook>`.

Sidecar-download image publication (no merge or deployment):
`registry.au.poloskov.net/ppoloskov/polka:multiple-libraries-browsing-7c6552a`
resolves to `sha256:2a286d3292880b0fbae7c77612886c5297ee2354967260ebe5ee51736d706b42`
for Linux/amd64, built from `codex/image-libraries-browsing` merge commit
`7c6552a` (all-of-the-above + PR #21 download re-embedding). Registry digest
matched the build metadata. Evidence:
`/private/tmp/polka-validation/sidecar-image2/{build-metadata.json,registry-manifest.json,registry-headers.txt}`.
The digest-pinned live deployment was unchanged by publishing.

Reader experience delivery on PR #23 (`8bcdbc4`, branch `codex/reader-experience`,
based on #22; image-branch commit `3268cf4`):
- P0 typography: Literata, PT Serif and Noto Serif bundled locally
  (fontsource packages, cyrillic+latin subsets, no CDN), justification,
  language-tagged automatic hyphenation (read-meta carries the book
  language and the reader container sets `lang`), first-line indent and a
  paragraph-spacing scale; all persist through the reader-prefs API.
- P1 navigation: in-book search (`GET /api/v1/read/{id}/search`, chapter
  matches with counts and snippets, in-chapter `<mark>` highlighting on
  jump), reading bookmarks in users.db (CRUD at
  `/api/v1/read/{id}/bookmarks`, same-spot saves update, localStorage
  fallback in public mode) with a ⚑ button and a panel tab, and a seekable
  progress bar weighted by per-chapter rendered sizes (read-meta also
  carries chapter sizes).
- Sync-mode desktop readers keep their existing routes; the new endpoints
  are server-mode only.
- IMPORTANT: the prior browsing tag `multiple-libraries-browsing-ce991fb`
  predates the reader feature core (its commit shipped without the search
  endpoint, bookmarks, seek bar and typography) — superseded by the reader
  image below.

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
(B4HNniqO vs C4L1KZe8) without an identified content difference — all user
features verified present (BookCard author links, BookListPage list-view
and grouping, SearchPage views); the as-found bundle bytes were lost to a
dist rebuild before comparison. RULE: every command in cross-repo sessions
must set workdir explicitly; never rely on sticky cwd.

Reader image publication (no merge or deployment):
`registry.au.poloskov.net/ppoloskov/polka:multiple-libraries-browsing-3268cf4`
resolves to `sha256:849ed2bc7c69a2bc7822a36ada5b6b8903bd71a09afb7ec2850e506658683842`
for Linux/amd64, built from `codex/image-libraries-browsing` commit `3268cf4`
(the complete browsing stack through #22 + the reader experience).
Registry digest matched the build metadata and differs from every earlier
browsing tag. Evidence:
`/private/tmp/reader-image/{build-metadata.json,registry-manifest.json,registry-headers.txt}`.
The digest-pinned live deployment was unchanged by publishing; this tag
supersedes all earlier browsing tags for deployment.

Review round 5 on PR #23 (`6511c14` / branch `ab01482`):
- The author endpoint fetches `limit+1` and trims before serializing (the
  earlier code fetched exactly `limit`, so `hasMore` was always false); the
  author page's load-more button is enabled. Probed at limit 2/3.
- Deletion deltas accumulate across the whole genre set (the scoped map was
  recreated per code, retaining only the last genre); Clear resets every
  library's scoped genre list transactionally. Probes: disabled-library
  deletion with two genres, Clear with a scoped key present.
- `segmentAt` resolves the total-length offset to the end of the last text
  node — capture results at the very end of a highlighted span restore
  exactly (the restore used to fail and fall back). Covered by the
  mark-fragment regression test at the span's total length.
- The PDF reader uses the same non-blocking save-failure banner.
- Reader search reuses the standard HTML tokenizer (x/net/html) for tag
  and entity stripping (inline elements join, block elements separate);
  tests cover beginning/end matches and multi-byte text. Removed the
  unused fullTextAt helper and segment start fields.
- Cross-repo discipline note: a round-5 commit briefly landed on the
  PRIMARY branch (`820bbdb`, containing the user's dirty WIP) via a
  sticky-cwd `git add -A`; recovered with `git reset 1dc5773` (the dirty
  working state is identical), the commit object is dangling and was never
  pushed. The round-5 work was then committed in the worktree correctly.
