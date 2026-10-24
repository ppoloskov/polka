import { afterEach, describe, expect, it, vi } from "vitest";
import {
  captureFB2Position,
  codePointOffset,
  restoreFB2Position,
  utf16Offset,
  matchingFB2Progress,
} from "./fb2Position";

afterEach(() => {
  vi.restoreAllMocks();
  document.body.innerHTML = "";
});

describe("FB2 source positions", () => {
  it("converts Cyrillic and supplementary Unicode offsets between crengine and DOM", () => {
    const text = "А😀Б";
    expect(codePointOffset(text, 3)).toBe(2);
    expect(utf16Offset(text, 2)).toBe(3);
  });

  it("captures a source text position and restores it after layout changes", () => {
    document.body.innerHTML =
      '<article><span data-fb2-path="/FictionBook[1]/body[1]/section[1]/p[1]/text()[1]">А😀БВ</span></article>';
    const root = document.querySelector("article");
    const span = root.firstChild;
    vi.spyOn(span, "getBoundingClientRect").mockReturnValue({
      top: 0,
      bottom: 400,
    });
    // Model the first two code points above the viewport's reading anchor.
    const positions = [];
    vi.spyOn(document, "createRange").mockImplementation(() => ({
      setStart: (_node, offset) => positions.push(offset),
      setEnd: vi.fn(),
      getBoundingClientRect: () => ({
        top: 800,
        bottom: positions.at(-1) < 3 ? 80 : 120,
      }),
    }));
    const scroll = vi.spyOn(window, "scrollTo").mockImplementation(() => {});
    const locator = captureFB2Position(root, 100);
    expect(locator).toMatch(/text\(\)\[1\]\.2$/);
    expect(restoreFB2Position(root, locator)).toBe(true);
    expect(positions.at(-1)).toBe(3);
    expect(scroll).toHaveBeenCalled();
  });

  it("does not restore a location from another source node", () => {
    document.body.innerHTML =
      '<article><span data-fb2-path="/FictionBook[1]/body[1]/p[1]/text()[1]">text</span></article>';
    expect(restoreFB2Position(document.body, "/FictionBook[1]/body[1]/p[2]/text()[1].0")).toBe(false);
  });
});

it("does not apply a bookmark to a different FB2 file", () => {
  const saved = { document: "a".repeat(32), locator: "/FictionBook[1]/body[1]/p[1]/text()[1].0" };
  expect(matchingFB2Progress(saved, "b".repeat(32))).toBeNull();
  expect(matchingFB2Progress(saved, saved.document)).toBe(saved);
});

it("captures the end of short visible text above the reading anchor", () => {
  document.body.innerHTML =
    '<article><span data-fb2-path="/FictionBook[1]/body[1]/p[1]/text()[1]">А😀Б</span></article>';
  const span = document.querySelector("span");
  vi.spyOn(span, "getBoundingClientRect").mockReturnValue({ top: 100, bottom: 120 });
  expect(captureFB2Position(document.querySelector("article"), 280)).toBe("/FictionBook[1]/body[1]/p[1]/text()[1].3");
});

// Search highlighting wraps text in <mark> elements; capture and restore
// must treat the span's text as one continuous space across the splits —
// including a locator at the very end of the last segment.
it("captures and restores positions through search-highlight marks", () => {
  document.body.innerHTML =
    '<article><span data-fb2-path="/FictionBook[1]/body[1]/p[1]/text()[1]">Первая часть и вторая</span></article>';
  // The shape the highlighter produces: text / mark / text.
  const span = document.querySelector("span");
  span.innerHTML = 'Первая <mark class="reader__hl">часть</mark> и вторая';
  vi.spyOn(span, "getBoundingClientRect").mockReturnValue({ top: 100, bottom: 120 });
  // jsdom ranges carry no rects; every boundary counts as above the anchor,
  // so the capture converges to the end of the span.
  const positions = [];
  vi.spyOn(document, "createRange").mockImplementation(() => ({
    setStart: (_node, offset) => positions.push(offset),
    setEnd: vi.fn(),
    getBoundingClientRect: () => ({ top: 800, bottom: 0 }),
  }));
  const locator = captureFB2Position(document.querySelector("article"), 280);
  const total = Array.from(span.textContent).length;
  expect(locator).toBe(`/FictionBook[1]/body[1]/p[1]/text()[1].${total}`);
  // The end-of-span boundary is restorable through the mark fragments.
  expect(restoreFB2Position(document.querySelector("article"), locator)).toBe(true);
  // The end boundary sits at the end of the LAST text segment (" и вторая").
  expect(positions.at(-1)).toBe(span.lastChild.textContent.length);
});
