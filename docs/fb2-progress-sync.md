# FB2 progress sync

Polka can translate FB2 reading positions between KOReader and the web reader.
Use the same FB2 file on both sides, the same Polka account, and configure
KOReader's Progress sync plugin with the device password from Polka's E-reader
dialog. KOReader controls when it pushes/pulls; this is not a live connection.
A manual pull on KOReader is useful when checking a browser update.

## Behavior

- KOReader updates resolve to the source text node and character offset in the
  web reader, including nested sections, headings, inline formatting, and verse.
- Browser updates publish a KOReader-compatible xpointer. Offsets count Unicode
  code points, independently of font size and screen width.
- The browser checks for newer progress on focus/visibility changes. If another
  reader changed the bookmark, it offers to continue there or keep the current
  position. Restoring a bookmark alone does not publish a new position.
- Saves carry a server revision. A stale browser save gets HTTP 409 and changes
  neither the web bookmark nor the KOReader bookmark. Backward moves are allowed.
- Opening an FB2 in the web reader computes its partial-MD5 fingerprint and
  registers its catalog mapping; a prior download is no longer required for
  subsequent device pushes. A push received before any mapping exists remains
  device-only until KOReader pushes again after the file is mapped.
- Stored FB2 locators include a version and file fingerprint. A position from a
  different byte-level FB2 variant is not reused as an exact bookmark. This
  identity travels in the opaque locator field during existing desktop sync.

## Implementation

`internal/library/fb2locator.go` observes source XML tokens before HTML conversion
and preserves crengine's normalized source paths. Literal XML whitespace is
collapsed before entity decoding, while entity-originated whitespace is retained.
Legacy encodings such as Windows-1251 are converted to UTF-8 before tracking the
source text. Inline spans carry these paths in `data-fb2-path`; headings expose
sanitized source HTML through the chapter API's `titleHtml` field.

`internal/server/reader.go` resolves web anchors against the parsed book rather
than trusting caller-supplied chapter/percentage values. `internal/server/kosync.go`
resolves incoming device anchors. Valid mapped updates write both progress tables
in one users.db transaction. The existing chapter/scroll fields remain available
for older clients. Browser-to-device percentages use text length; KOReader's
rendered-page percentage can differ even when the text location matches.

The web progress API additionally returns `revision` and, for scoped FB2 locators,
`document`. FB2 saves include `revision`, `digest`, and the plain xpointer in
`locator`. A mismatched file fingerprint returns HTTP 412. The kosync wire format
is unchanged.

## Limits and validation

Exact restoration covers mapped main-body text in the server web reader. The
whole-book body title remains omitted to preserve legacy chapter numbering. Footnote reference text is
mapped; a location inside a separate notes/comments body is not translated to the
web reader's modal footnote. Unmapped device locations retain kosync support and
update the shelf percentage, clearing any previous exact web locator.

crengine splits raw text runs at 8192 characters before entity decoding. This
implementation conservatively omits exact anchors for those long runs and their
ambiguous direct text siblings, instead of generating potentially invalid text
indexes. Source locations in ordinary neighboring/inline text remain available.
Older crengine paths containing internal boxing nodes are also not translated.
EPUB, TXT, and PDF do not gain position translation in this change.

The parser fixture `internal/library/testdata/kosync-crengine.json` was generated
by parsing `kosync.fb2` with KOReader crengine commit
`ab1981c22e4e67cf9d98be3c301736941d2d271c`, walking its text nodes, and recording
`ldomXPointer(node, 0).toString()` alongside node text. Tests compare every emitted
Polka source-text span against that independent output. A generated Polka pointer
was also resolved through `ldomDocument::createXPointer`. The harness used a
non-rendering build with optional font/image libraries disabled.

Automated tests cover the HTTP round trip, revisions, fingerprint mismatches,
nested chapter splitting, entities, CDATA, Cyrillic, supplementary Unicode,
Windows-1251, and browser restoration without a sync loop. These checks do not
replace a physical-device test of pagination and KOReader sync settings.

Before release, use the same FB2 on a KOReader device and this server:

1. Push from a mid-paragraph device position, open the browser reader, and check
   the passage. Change browser fonts/width and reopen to check restoration.
2. Read further in the browser, pull on KOReader, and check the passage again.
3. Repeat with nested sections, verse, inline formatting, and Cyrillic text.
4. Leave the browser open, advance on the device, return to the browser, and
   check both conflict choices. Also test an intentional backward move.

## Upgrading existing user databases

Startup adds missing progress columns in a checked transaction. Existing users,
bookmarks, chapter/scroll positions, and locator values are preserved; old records
start at revision zero. Migration errors stop startup instead of being ignored.
Explicit chapter navigation saves after rendering, while restoring a remote
bookmark remains read-only. Short visible text above the reading anchor also
produces a valid bookmark.
