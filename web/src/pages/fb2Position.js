// crengine offsets count Unicode code points; DOM ranges count UTF-16 units.
export const codePointOffset = (text, utf16Offset) => Array.from(text.slice(0, utf16Offset)).length;
export const utf16Offset = (text, offset) => Array.from(text).slice(0, offset).join("").length;

// Text segments of a span in document order. Search highlighting wraps
// text in <mark> elements, so the source text may be split across several
// text nodes (plus elements); its total content is preserved, and capture
// and restoration treat the segments as one continuous offset space.
const spanTextSegments = (span) => {
  const segments = [];
  const walker = document.createTreeWalker(span, NodeFilter.SHOW_TEXT);
  let start = 0;
  let node;
  while ((node = walker.nextNode())) {
    if (node.textContent.length > 0) {
      segments.push({ node, start });
      start += Array.from(node.textContent).length;
    }
  }
  return { segments, length: start };
};

// The text node and child offset for a code-point index in the span's
// continuous text space (null when out of range).
const segmentAt = (segments, codeIndex) => {
  let scanned = 0;
  for (let i = 0; i < segments.length; i++) {
    const chars = Array.from(segments[i].node.textContent);
    if (codeIndex < scanned + chars.length) {
      // The child offset is the UTF-16 length of the preceding code points.
      return { node: segments[i].node, offset: chars.slice(0, codeIndex - scanned).join("").length };
    }
    scanned += chars.length;
  }
  return null;
};

export function captureFB2Position(root, viewportY = window.innerHeight * 0.35) {
  if (!root) return "";
  const spans = Array.from(root.querySelectorAll("[data-fb2-path]"));
  let span =
    spans.find((el) => {
      const rect = el.getBoundingClientRect();
      return rect.top <= viewportY && rect.bottom > viewportY;
    }) ??
    spans.find((el) => {
      const top = el.getBoundingClientRect().top;
      return top >= viewportY && top < window.innerHeight;
    });
  // A short chapter may fit entirely above the reading anchor. Keep its
  // last visible text position instead of silently dropping the save.
  const atEnd = !span;
  if (atEnd) {
    span = spans.filter((el) => {
      const rect = el.getBoundingClientRect();
      return rect.bottom > 0 && rect.top < window.innerHeight;
    }).at(-1);
  }
  if (!span) return "";
  const { segments, length } = spanTextSegments(span);
  if (segments.length === 0 || length === 0) return "";
  if (atEnd) return `${span.dataset.fb2Path}.${length}`;
  // Pick the beginning of the text line at the reading anchor. This remains
  // meaningful when a font change moves that line on the next device. Offsets
  // are code-point indices, so a boundary can never split a surrogate pair.
  let low = 0;
  let high = length;
  const range = document.createRange();
  while (low < high) {
    const mid = Math.floor((low + high) / 2);
    const at = segmentAt(segments, mid);
    if (!at) break;
    const next = segmentAt(segments, mid + 1);
    if (!next) break;
    range.setStart(at.node, at.offset);
    range.setEnd(next.node, next.offset);
    if (range.getBoundingClientRect().bottom <= viewportY) low = mid + 1;
    else high = mid;
  }
  if (low >= length) return `${span.dataset.fb2Path}.${length}`;
  if (!segmentAt(segments, low)) return "";
  return `${span.dataset.fb2Path}.${low}`;
}

// The code point at a code-point index (for surrogate-pair checks).
const fullTextAt = (segments, index) => {
  let scanned = 0;
  for (const seg of segments) {
    const chars = Array.from(seg.node.textContent);
    if (index < scanned + chars.length) return chars[index - scanned];
    scanned += chars.length;
  }
  return null;
};

export function restoreFB2Position(root, locator) {
  const match = /^(.*)\.(\d+)$/.exec(locator ?? "");
  if (!match || !root) return false;
  const span = Array.from(root.querySelectorAll("[data-fb2-path]")).find((el) => el.dataset.fb2Path === match[1]);
  if (!span) return false;
  const { segments, length } = spanTextSegments(span);
  if (segments.length === 0) return false;
  const index = Math.min(Number(match[2]), length);
  const at = segmentAt(segments, index);
  if (!at) return false;
  const next = segmentAt(segments, index + 1 <= length ? index + 1 : index);
  if (!next) return false;
  const range = document.createRange();
  range.setStart(at.node, at.offset);
  range.setEnd(next.node, next.offset);
  const rect = range.getBoundingClientRect();
  window.scrollTo(0, Math.max(0, window.scrollY + rect.top - window.innerHeight * 0.35));
  return true;
}

// A bookmark from a different byte-level FB2 variant is not transferable.
export const matchingFB2Progress = (progress, digest) =>
  digest && progress?.document && progress.document !== digest ? null : progress;
