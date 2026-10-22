import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useNavigate } from "react-router-dom";
import { t } from "../i18n";
import BookListPage from "./BookListPage";

vi.mock("../api/fetchBooks", () => ({ fetchAuthorBooks: vi.fn(), fetchSeriesBooks: vi.fn(), fetchShelfBooks: vi.fn() }));
vi.mock("../api/manage", () => ({ exportUrl: (ids) => `/Images/export?ids=${ids.join(",")}` }));
import { fetchAuthorBooks, fetchSeriesBooks, fetchShelfBooks } from "../api/fetchBooks";

describe("BookListPage library scope", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    fetchShelfBooks
      .mockResolvedValueOnce({
        titlesList: [
          { BookID: 1, Title: "Available book", Available: true },
          { BookID: 2, Title: "Missing book", Available: false },
        ],
        hasMore: true,
        nextOffset: 2,
      })
      .mockResolvedValue({ titlesList: [], hasMore: false });
  });

  it("scopes the initial and paged fetches and exports only available rows", async () => {
    render(
      <MemoryRouter initialEntries={["/shelf/newest"]}>
        <Routes><Route path="/shelf/:shelfId" element={<BookListPage kind="shelf" selectedLibraryId="north" />} /></Routes>
      </MemoryRouter>
    );
    await screen.findByText("Available book");
    expect(fetchShelfBooks).toHaveBeenCalledWith({ shelfId: "newest", libraryId: "north" });
    expect(screen.getByRole("link", { name: t("downloadAll") })).toHaveAttribute("href", "/Images/export?ids=1");
    fireEvent.click(screen.getByRole("button", { name: t("lists.more") }));
    await waitFor(() => expect(fetchShelfBooks).toHaveBeenLastCalledWith({ shelfId: "newest", offset: 2, libraryId: "north" }));
  });

  it("does not append a pending page from the previous library after switching scope", async () => {
    let resolveOldPage;
    fetchShelfBooks.mockReset();
    fetchShelfBooks
      .mockResolvedValueOnce({ titlesList: [{ BookID: 1, Title: "North first" }], hasMore: true, nextOffset: 1 })
      .mockImplementationOnce(() => new Promise((resolve) => { resolveOldPage = resolve; }))
      .mockResolvedValueOnce({ titlesList: [{ BookID: 3, Title: "South first" }], hasMore: false });

    const ScopedList = ({ libraryId }) => (
      <BookListPage key={libraryId} kind="shelf" selectedLibraryId={libraryId} />
    );
    const { rerender } = render(
      <MemoryRouter initialEntries={["/shelf/newest"]}>
        <Routes><Route path="/shelf/:shelfId" element={<ScopedList libraryId="north" />} /></Routes>
      </MemoryRouter>
    );
    await screen.findByText("North first");
    fireEvent.click(screen.getByRole("button", { name: t("lists.more") }));
    rerender(
      <MemoryRouter initialEntries={["/shelf/newest"]}>
        <Routes><Route path="/shelf/:shelfId" element={<ScopedList libraryId="south" />} /></Routes>
      </MemoryRouter>
    );
    await screen.findByText("South first");
    resolveOldPage({ titlesList: [{ BookID: 2, Title: "North late page" }], hasMore: false });
    expect(await screen.findByText("South first")).toBeInTheDocument();
    expect(screen.queryByText("North late page")).toBeNull();
  });
});

const book = (BookID, extras = {}) => ({ BookID, Title: `Book ${BookID}`, AuthorsNames: "Comma, Name", Authors: [{ AuthorID: 4, Name: "Comma, Name" }], ...extras });

function Navigation() {
  const navigate = useNavigate();
  return <button type="button" onClick={() => navigate("/author/next")}>Next author</button>;
}

function renderAuthor(id = "1") {
  return render(
    <MemoryRouter initialEntries={[`/author/${id}`]}>
      <Navigation />
      <Routes>
        <Route path="/author/:authorId" element={<BookListPage kind="author" />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("BookListPage browsing controls", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    localStorage.clear();
    fetchAuthorBooks.mockReset();
    fetchSeriesBooks.mockReset();
    fetchShelfBooks.mockReset();
  });

  it("filters an author page, groups series without colliding with the translated empty label, and exports only visible books", async () => {
    fetchAuthorBooks.mockResolvedValue({ titlesList: [
      book(1, { Lang: "en", SeriesTitle: "Alpha" }),
      book(2, { Lang: "ru", SeriesTitle: "Alpha" }),
      book(3, { Lang: "en", SeriesTitle: "Not in a series" }),
      book(4, { Lang: "en", SeriesTitle: "" }),
      book(5, { Lang: "en", SeriesTitle: "constructor" }),
      book(6, { Lang: "en", SeriesTitle: "__proto__" }),
      book(7, { Lang: "en", SeriesTitle: "toString" }),
    ] });
    renderAuthor();

    await screen.findByText("Book 1");
    fireEvent.change(screen.getByLabelText(/Language/i), { target: { value: "en" } });
    expect(screen.getByRole("link", { name: /download all/i })).toHaveAttribute("href", "/Images/export?ids=1,3,4,5,6,7");
    expect(screen.queryByText("Book 2")).not.toBeInTheDocument();
    expect(screen.getAllByRole("heading", { level: 2 })).toHaveLength(6);
  });

  it("shows no download link when filtering leaves no books and resets a stale language on author navigation", async () => {
    fetchAuthorBooks.mockResolvedValueOnce({ titlesList: [book(1, { Lang: "ru" })] })
      .mockResolvedValueOnce({ titlesList: [book(2, { Lang: "en" })] });
    const view = renderAuthor();
    await screen.findByText("Book 1");
    fireEvent.change(screen.getByLabelText(/Language/i), { target: { value: "ru" } });
    expect(screen.getByRole("link", { name: /download all/i })).toHaveAttribute("href", "/Images/export?ids=1");
    fireEvent.click(screen.getByRole("button", { name: "Next author" }));
    await screen.findByText("Book 2");
    expect(screen.getByRole("link", { name: /download all/i })).toHaveAttribute("href", "/Images/export?ids=2");
  });

  it("does not offer a bulk download for an empty result", async () => {
    fetchAuthorBooks.mockResolvedValue({ titlesList: [] });
    renderAuthor();
    await screen.findByText(/no books/i);
    expect(screen.queryByRole("link", { name: /download all/i })).not.toBeInTheDocument();
  });

  it("uses structured author IDs even when the author name contains a comma", async () => {
    fetchAuthorBooks.mockResolvedValue({ titlesList: [book(1)] });
    renderAuthor();
    const author = await screen.findByRole("link", { name: "Comma, Name" });
    expect(author).toHaveAttribute("href", "/author/4");
  });

  it("keeps rendering when remembered-view storage is denied", async () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => { throw new Error("denied"); });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("denied"); });
    fetchAuthorBooks.mockResolvedValue({ titlesList: [book(1)] });
    renderAuthor();
    await screen.findByText("Book 1");
    fireEvent.click(screen.getByRole("button", { name: /list/i }));
    expect(screen.getByText("Book 1")).toBeInTheDocument();
  });
});
