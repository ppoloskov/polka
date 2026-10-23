import { act, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import ReaderPage from "./ReaderPage";
import { t } from "../i18n";
import { fetchProgress, saveFB2Progress, fetchReadMeta, fetchChapter, saveProgress } from "../api/reader";

const locator = "/FictionBook[1]/body[1]/section[1]/p[1]/text()[1].2";
vi.mock("../api/reader", () => ({
  fetchReadMeta: vi.fn(async () => ({
    format: "fb2",
    digest: "a".repeat(32),
    chapters: [{ title: "Chapter" }],
    notes: {},
  })),
  fetchChapter: vi.fn(async () => ({
    index: 0,
    html: '<p><span data-fb2-path="/FictionBook[1]/body[1]/section[1]/p[1]/text()[1]">Reading text</span></p>',
  })),
  fetchProgress: vi.fn(),
  fetchReaderPrefs: vi.fn(async () => ({})),
  saveFB2Progress: vi.fn(),
  saveProgress: vi.fn(),
  saveReaderPrefs: vi.fn(async () => ({})),
  localProgress: { get: vi.fn(), set: vi.fn() },
}));

const flush = async (ms = 100) => {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
};

beforeEach(() => {
  vi.useFakeTimers();
  fetchReadMeta.mockResolvedValue({
    format: "fb2",
    digest: "a".repeat(32),
    chapters: [{ title: "Chapter" }],
    notes: {},
  });
  fetchChapter.mockImplementation(async (_bookId, index) => ({
    index,
    html: `<p><span data-fb2-path="/FictionBook[1]/body[1]/section[${index + 1}]/p[1]/text()[1]">Reading text</span></p>`,
  }));
  saveProgress.mockClear();
  vi.stubGlobal("matchMedia", () => ({ matches: false }));
  vi.stubGlobal(
    "IntersectionObserver",
    class {
      observe() {}
      disconnect() {}
    },
  );
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
  vi.spyOn(Element.prototype, "getBoundingClientRect").mockReturnValue({
    top: 0,
    bottom: 1000,
  });
  vi.spyOn(document, "createRange").mockImplementation(() => ({
    setStart: vi.fn(),
    setEnd: vi.fn(),
    getBoundingClientRect: () => ({ top: 100, bottom: 1000 }),
  }));
  fetchProgress.mockResolvedValue({
    stored: true,
    revision: 4,
    chapter: 0,
    position: 0,
    locator,
  });
  saveFB2Progress.mockResolvedValue({ stored: true, revision: 5 });
  saveFB2Progress.mockClear();
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const open = async () => {
  render(
    <MemoryRouter initialEntries={["/read/1"]}>
      <Routes>
        <Route path="/read/:bookId" element={<ReaderPage />} />
      </Routes>
    </MemoryRouter>,
  );
  await flush();
};

it("hides chapter images that fail to load", async () => {
  fetchChapter.mockImplementation(async (_bookId, index) => ({
    index,
    html: `<p><img class="fb2-img" src="/api/v1/read/1/img/${index}"/></p>`,
  }));
  await open();
  const img = document.querySelector(".reader__content img");
  expect(img).not.toBeNull();
  fireEvent.error(img);
  expect(img.classList.contains("fb2-img-missing")).toBe(true);
});

it("restores device progress without publishing a browser update", async () => {
  await open();
  await flush(1500);
  expect(window.scrollTo).toHaveBeenCalled();
  expect(saveFB2Progress).not.toHaveBeenCalled();
  fireEvent.wheel(window);
  fireEvent.scroll(window);
  await flush(1500);
  expect(saveFB2Progress).toHaveBeenCalledWith(
    "1",
    expect.objectContaining({
      locator: expect.stringContaining("/text()[1]."),
    }),
    4,
    "a".repeat(32),
  );
});

it("blocks stale browser updates until the reader chooses a position", async () => {
  await open();
  fetchProgress.mockResolvedValue({
    stored: true,
    revision: 6,
    chapter: 0,
    position: 0,
    locator,
  });
  fireEvent.focus(window);
  await flush();
  expect(screen.getByText(t("reader.syncChanged"))).toBeInTheDocument();
  fireEvent.wheel(window);
  fireEvent.scroll(window);
  await flush(1500);
  expect(saveFB2Progress).not.toHaveBeenCalled();
  fireEvent.click(screen.getByText(t("reader.syncContinue")));
  await flush(1500);
  expect(screen.queryByText(t("reader.syncChanged"))).not.toBeInTheDocument();
  expect(saveFB2Progress).not.toHaveBeenCalled();
});

it.each(["fb2", "txt", "epub"])("saves explicit %s chapter navigation even without a scroll event", async (format) => {
  fetchReadMeta.mockResolvedValue({
    format,
    digest: format === "fb2" ? "a".repeat(32) : "",
    chapters: [{ title: "First" }, { title: "Second" }],
    notes: {},
  });
  await open();
  await flush(1500);
  saveFB2Progress.mockClear();
  saveProgress.mockClear();
  fireEvent.keyDown(window, { key: "ArrowRight" });
  await flush();
  await flush(1500);
  if (format === "fb2") {
    expect(saveFB2Progress).toHaveBeenCalledWith(
      "1",
      expect.objectContaining({ chapter: 1, locator: expect.stringContaining("/section[2]/") }),
      4,
      "a".repeat(32),
    );
  } else {
    expect(saveProgress).toHaveBeenCalledWith("1", 1, 0, 0.5);
  }
});
