import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { t } from "../i18n";
import BookListPage from "./BookListPage";

vi.mock("../api/fetchBooks", () => ({ fetchAuthorBooks: vi.fn(), fetchSeriesBooks: vi.fn(), fetchShelfBooks: vi.fn() }));
vi.mock("../api/manage", () => ({ exportUrl: (ids) => `/Images/export?ids=${ids.join(",")}` }));
import { fetchShelfBooks } from "../api/fetchBooks";

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
