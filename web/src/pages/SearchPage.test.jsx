import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
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
