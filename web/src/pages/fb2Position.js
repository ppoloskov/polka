// crengine offsets count Unicode code points; DOM ranges count UTF-16 units.
export const codePointOffset = (text, utf16Offset) => Array.from(text.slice(0, utf16Offset)).length;
export const utf16Offset = (text, offset) => Array.from(text).slice(0, offset).join("").length;

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
  if (!span?.firstChild || span.firstChild.nodeType !== 3) return "";
  const node = span.firstChild;
  if (atEnd) return `${span.dataset.fb2Path}.${Array.from(node.textContent).length}`;
  // Pick the beginning of the text line at the reading anchor. This remains
  // meaningful when a font change moves that line on the next device.
  let low = 0;
  let high = node.textContent.length;
  const range = document.createRange();
  while (low < high) {
    const mid = Math.floor((low + high) / 2);
    range.setStart(node, mid);
    range.setEnd(node, Math.min(mid + 1, node.length));
    if (range.getBoundingClientRect().bottom <= viewportY) low = mid + 1;
    else high = mid;
  }
  // Don't cut a surrogate pair in half.
  if (low > 0 && /[\uDC00-\uDFFF]/.test(node.textContent[low] ?? "")) low--;
  return `${span.dataset.fb2Path}.${codePointOffset(node.textContent, low)}`;
}

export function restoreFB2Position(root, locator) {
  const match = /^(.*)\.(\d+)$/.exec(locator ?? "");
  if (!match || !root) return false;
  const span = Array.from(root.querySelectorAll("[data-fb2-path]")).find((el) => el.dataset.fb2Path === match[1]);
  const node = span?.firstChild;
  if (!node || node.nodeType !== 3) return false;
  const offset = utf16Offset(node.textContent, Number(match[2]));
  const range = document.createRange();
  range.setStart(node, offset);
  range.setEnd(node, Math.min(offset + (node.textContent.codePointAt(offset) > 0xffff ? 2 : 1), node.length));
  const rect = range.getBoundingClientRect();
  window.scrollTo(0, Math.max(0, window.scrollY + rect.top - window.innerHeight * 0.35));
  return true;
}

// A bookmark from a different byte-level FB2 variant is not transferable.
export const matchingFB2Progress = (progress, digest) =>
  digest && progress?.document && progress.document !== digest ? null : progress;
