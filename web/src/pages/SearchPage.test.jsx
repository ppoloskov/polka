import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import SearchPage from "./SearchPage";

vi.mock("../api/fetchBooks", () => ({
  fetchCatalogShelves: vi.fn(), fetchSearchAuthors: vi.fn(), fetchSearchGenres: vi.fn(),
  fetchSearchSeries: vi.fn(), fetchSearchStats: vi.fn(), fetchSearchTitles: vi.fn(),
}));
import { fetchSearchStats, fetchSearchTitles } from "../api/fetchBooks";

describe("SearchPage library scope", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    fetchSearchStats.mockResolvedValue({ searchStats: {} });
    fetchSearchTitles.mockResolvedValue({ titlesList: [] });
  });

  it("refetches statistics and titles when the selected library changes", async () => {
    const renderSearch = (libraryId) => (
      <MemoryRouter initialEntries={["/search?q=space"]}>
        <SearchPage config={{ groups: [] }} selectedLibraryId={libraryId} />
      </MemoryRouter>
    );
    const view = render(renderSearch("north"));
    await waitFor(() => expect(fetchSearchTitles).toHaveBeenCalledWith({ search: "space", selectedGroupID: null, libraryId: "north" }));
    expect(fetchSearchStats).toHaveBeenCalledWith({ search: "space", selectedGroupID: null, libraryId: "north" });

    view.rerender(renderSearch("south"));
    await waitFor(() => expect(fetchSearchTitles).toHaveBeenCalledWith({ search: "space", selectedGroupID: null, libraryId: "south" }));
  });
});

function renderSearch() {
  return render(
    <MemoryRouter initialEntries={["/search?q=polka"]}>
      <Routes><Route path="/search" element={<SearchPage config={{}} />} /></Routes>
    </MemoryRouter>,
  );
}

describe("SearchPage book view", () => {
  beforeEach(() => {
    localStorage.clear();
    fetchSearchStats.mockResolvedValue({ searchStats: { bookTitles: 1 } });
    fetchSearchTitles.mockResolvedValue({ titlesList: [{ BookID: 8, Title: "A result" }] });
  });

  it("persists the list choice for a later search page mount", async () => {
    const first = renderSearch();
    const listButton = await screen.findByRole("button", { name: "List" });
    fireEvent.click(listButton);
    expect(screen.getByRole("article")).toHaveClass("book-card--list");
    first.unmount();
    renderSearch();
    await screen.findByRole("article");
    expect(screen.getByRole("article")).toHaveClass("book-card--list");
  });
});
