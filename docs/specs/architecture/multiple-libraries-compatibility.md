# Multiple-library compatibility contract

Approved implementation direction: user request on 2026-10-22; minimal Luna agent
implementation as validated PRs, preserving existing image-only upgrades.
Request: `docs/specs/requests/multiple-libraries.md`. This compact contract governs
the implementation series; decisions below supersede speculative request details
from the earlier planning discussion retained in the primary checkout.

Result: approve incremental implementation under these boundaries. Every PR
must leave existing installations usable. New feature paths ship only when
their public workflows and failure behavior are validated.

| Current behavior | New behavior | Breaking? | Compatibility decision | Test required |
| --- | --- | --- | --- | --- |
| One catalog, numeric user refs | One catalog, library-scoped source keys, same public IDs | No | Never renumber; seed a durable allocation floor from catalog and users refs | Old schema/reopen/orphan references/all writers |
| Dynamic library-dir flag/env | Legacy binding uses same dynamic root; new roots are explicit | No | No migration file probing, copy, scan or onboarding | Same config, missing source, changed legacy root |
| Existing upload/import requests | Thin adapters to default legacy target | No | No new required parameter; never clear other libraries | Old multipart/CLI requests and polling |
| Readers/OPDS/desktop sync | Existing routes/payloads plus optional library data | No | No protocol change or mandatory client upgrade | Old clients, progress, downloads/reader |
| Destructive replace | Stage target snapshot then one transaction | Safer semantics | Keep uncertain old entries/state; no fuzzy identity reassignment | Parse/read/cancel/commit failure/retry |
| Admin deleted flag | Missing state separate from admin deletion | No | Source refresh cannot unhide an admin-deleted entry | Remove/reappear/delete/restore |
| Missing sources | Last published catalog remains | No | A mount failure cannot publish removals | Unreadable root/partial scan/empty result |

## Identity decisions

Library IDs are durable opaque keys. Book IDs are global monotonically allocated
integers, independent of source numeric IDs. Retain allocation state and missing
rows; updates never physically delete source files. Seed known numeric user
references through auth's own query API before serving/importing.

Guarantee no ID recycling from the known migration boundary forward. Prior
aliasing and offline-only lost references are unknowable preexisting risks,
not release blockers requiring a new sync protocol. Never infer their identity.

Folder identity is relative path plus full file SHA-256. Byte changes or moves
produce a new entry in v1; content-based rename matching is deferred to keep
review small. Staged keys represent immutable records: a matching key retains
metadata/relations, while a changed key creates a new entry. Source presence is
separate from the key and can change without reallocating. The initial INPX
adapter uses an exact full-record fingerprint (excluding its deletion flag),
scoped to the registered library. Source-ID-only continuity is deferred without
a verified provider guarantee. Metadata/locator corrections conservatively become
remove/add in v1. The INPX index is authoritative for record membership: this
does not verify unchanged archive bytes or detect dump changes absent from the
index. No full dump hashing is promised. Fuzzy title/author, filename, whole-index
hash and kosync partial MD5 cannot establish continuity. Unknown legacy entries
are never automatically matched.

Preserve working legacy progress/digests at migration. New byte-changed files
receive new IDs, preventing old locators from applying to them. Root resolution
must cover reader, cover, download, export, email and lazy metadata paths.

## Minimality and proof

Reuse SQLite and existing helpers; no per-library databases, dependency changes,
new job framework, or copied book dumps. Disk-backed staging precedes atomic
target publication. Keep useful status only; durable generic run history/cancel
workflows are deferred. Empty updates must fail safely unless explicitly allowed.

Source inspection found no folder scanner. Existing AddBook relations/search
logic and FB2/EPUB parsers should be reused. Current batch import drops global
indexes and clears catalog state; do not reuse those destructive operations for
independent updates. Store migration needs a newer-schema guard.

Upgrade proof must include the public server workflows and separate users DB,
not only schema fixtures. Docker is absent locally: native restart acceptance
can validate data/config behavior but actual image acceptance remains explicit.
Every accepted batch records red/green tests, review, and remaining limits.

Separate Polka reading state is keyed by public book ID and is tested with
byte-identical FB2 copies. KOReader uses a document digest without library
identity; its existing one-digest bridge can be ambiguous for identical copies.
No reader protocol or digest mapping redesign is included in this series.
