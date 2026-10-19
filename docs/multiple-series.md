# Multiple series per book

Catalog schema v5 adds `book_series` and its reverse lookup index. The migration
runs in a transaction and backfills each existing primary series, preserving its
ID and sequence number. Primary-series columns remain for compatibility with
existing cards, recommendations, and clients.

FB2 uploads retain all title-info sequences, including nested sequences. Existing
catalog books acquire additional memberships when their book page reads the FB2
metadata. No reimport is required; books whose source files are unavailable retain
their existing primary series until the files become available.

Each series page uses its own membership number for display and ordering. Search,
series counts, bulk imports, deleted-book restoration, and catalog clearing include
all memberships. Repeated metadata reads do not duplicate memberships; absent
sequence numbers do not erase known catalog numbers.

Tests cover v4 upgrades and reopening, preservation of series IDs/numbers, uploads,
bulk imports, secondary-series ordering/search, and lazy metadata enrichment.
