import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./api", () => ({ default: { getJson: vi.fn() } }));
import api from "./api";
import { fetchHomeShelves, fetchSearchTitles, fetchShelfBooks, fetchSimilarBooks, fetchLibraries } from "./fetchBooks";

describe("catalog API library scope", () => {
  beforeEach(() => vi.clearAllMocks());

  it("loads the protected library list without requiring a response shape", async () => {
    api.getJson.mockResolvedValue({ libraries: [] });
    await fetchLibraries();
    expect(api.getJson).toHaveBeenCalledWith("api/v1/libraries");
  });

  it("adds libraryId to shelf, search, and pagination requests only when selected", () => {
    fetchHomeShelves({ libraryId: "north" });
    fetchSearchTitles({ search: "Dune", selectedGroupID: 4, libraryId: "north" });
    fetchShelfBooks({ shelfId: "newest", offset: 20, libraryId: "north" });
    fetchSimilarBooks({ bookId: 4, title: "Dune", author: "Herbert", libraryId: "north" });
    fetchHomeShelves();

    expect(api.getJson).toHaveBeenNthCalledWith(1, "main/getBooks/getHomeShelves?libraryId=north");
    expect(api.getJson).toHaveBeenNthCalledWith(2, "main/getBooks/getSearchTitles?search=Dune&selectedGroupID=4&libraryId=north");
    expect(api.getJson).toHaveBeenNthCalledWith(3, "main/getBooks/getShelfBooks?shelfId=newest&offset=20&libraryId=north");
    expect(api.getJson).toHaveBeenNthCalledWith(4, "main/getBooks/getSimilarBooks?bookId=4&title=Dune&author=Herbert&libraryId=north");
    expect(api.getJson).toHaveBeenNthCalledWith(5, "main/getBooks/getHomeShelves");
  });
});
