# Polka agent context

## North star

Polka is a private, reliable e-book library. Preserve user data and stable public
book IDs while keeping catalog updates and browsing simple and fast.

## Boundaries

- `internal/store` owns catalog schema/queries; `internal/auth` owns personal
  state in `users.db`. Do not renumber existing books or fuzzy-match identities.
- Existing installations must work after changing only the image: keep flags,
  environment, mounts, uploads, routes, clients and user state. No startup scan,
  reimport, source setup, new mount or client upgrade may be required.
- New libraries are INPX plus dump roots or recursive folder trees. Updates are
  manual; copies in different libraries keep separate entries and reading state.
- Keep changes slim, preserve the original Go/React approach, reuse helpers and
  SQLite transactions, and add no dependency or generic service/job framework.
- Preserve unrelated working-tree files. Exclude independent browsing and FB2
  kosync changes from this series. Do not modify reader protocols.
- Keep README.md, AGENTS.md and handover.md current. Use focused behavioral tests
  before implementation; run surrounding suites before declaring a PR validated.

## Delivery

See `docs/specs/planned/multiple-libraries-work-queue.md` for the staged PR series.
Architect reviews each diff and evidence; the Git agent publishes reviewed
branches to `ppoloskov/polka`. No merge is authorized.

Library management reuses the existing import mutex/status. Source paths and
kinds are fixed after registration; changing a root requires a new namespace.
Keep public library metadata free of server paths. Successful updates and
enable/disable changes must invalidate genre counts; update cover caches by owner.

Catalog libraryId filters must apply before LIMIT/OFFSET, including personal
shelves and external collection references. Book column additions must stay
aligned across every scan. Personal metadata lookup may retain missing/disabled
entries; ordinary catalog queries and file serving must remain availability-gated.

Upload duplicate checks are scoped to the default legacy target; a copy in a
named library must not block an independent default-library upload. Keep ordinary
Polka progress per book ID; KOReader digest ambiguity is a documented external
protocol limit. Leave the old bulk importer as test-fixture support in this series.
