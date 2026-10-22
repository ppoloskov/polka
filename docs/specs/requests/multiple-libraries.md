# Persistent libraries

User request, 2026-10-22: replace destructive one-off INPX imports with persistent
libraries that an administrator updates manually. Two types: INPX plus book
dumps, and a recursive directory of book files. Detect additions/removals without
ID clashes. Copies in different libraries retain separate entries and reading
state. Existing installations must work after changing only the image.

Implementation authorized: Luna agents, slim original-style changes, a series
of validated PRs. Do not merge automatically. Compatibility decisions are in
`../architecture/multiple-libraries-compatibility.md`; packages and evidence are
in `../planned/multiple-libraries-work-queue.md`.

## User outcomes

- Existing deployments retain flags, env, mounts, IDs, users, reader state,
  URLs, uploads, imports, OPDS, auth and old clients. Automatic additive migration
  creates one default legacy binding without source probing/reimport/onboarding.
- Admin manages named libraries and requests updates. New INPX libraries save
  source path and books root; folder libraries recurse beneath a saved root.
- Readers browse all enabled libraries or filter by one. Cards/details show
  source. Personal lists/history retain missing entries as unavailable.
- Staging validates a complete source before one atomic target-only publication.
  An unreadable/partial source or failed update keeps the published catalog.
- Source disappearance retains identity and personal state; admin deletion is
  separate. No automatic source refresh restores a deliberately hidden book.
- Library IDs scope source keys; public numeric book IDs are globally allocated
  and never recycled from the known persisted migration boundary onward.

## Acceptance

Upgrade existing supported schemas and separate users DB using unchanged config;
restart twice, use old clients, upload/download/read, list/rate/save progress,
exercise old CLI/web import contracts. Add two libraries with equal source IDs
and locators: files/state must remain distinct. Repeat unchanged updates with
zero additions/removals; add/remove/change input and inspect durable diff. Fail
parsing/traversal/publication, retry and restart: see old or new complete catalog.
Check recursive metadata/fallback, root confinement, ZIP/7z INPX access, source
filter counts/pagination/bulk downloads, missing personal entries and auth modes.

Tests start at real migration/query boundaries and include public CLI/HTTP
workflows. Full Go/Node/build/vet checks follow focused red/green tests. Actual
container upgrade acceptance is distinct from native executable/data checks.

## Non-goals and limits

No watchers/schedules, cross-library dedup/shared state, source file deletion,
new metadata provider, per-library DB/ACL, generic job framework or reader-sync
protocol redesign. Folder rename-by-content recovery is deferred; changed/moved
files conservatively receive new IDs. Never fuzzy-match user references.
Lost/reused historical IDs and references only on offline clients cannot be
reconstructed; preserve known state without claiming automatic repair.

Status: approved for incremental implementation by the user's explicit request.
