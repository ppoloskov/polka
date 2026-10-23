import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { t } from "./i18n";
import Root from "./Root";

vi.mock("./api/auth", () => ({ fetchMe: vi.fn(), logout: vi.fn() }));
vi.mock("./api/fetchBooks", () => ({
  fetchConfig: vi.fn(), fetchLibraries: vi.fn(), fetchHomeShelves: vi.fn(), fetchCatalogShelves: vi.fn(),
  fetchShelfBooks: vi.fn(), fetchSearchStats: vi.fn(), fetchSearchTitles: vi.fn(), fetchSearchAuthors: vi.fn(),
  fetchSearchGenres: vi.fn(), fetchSearchSeries: vi.fn(), fetchAuthorBooks: vi.fn(), fetchSeriesBooks: vi.fn(),
  fetchBookForm: vi.fn(), fetchExternalEnrichment: vi.fn(), fetchSimilarBooks: vi.fn(),
}));
vi.mock("./pages/HomePage", () => ({ default: ({ selectedLibraryId }) => <div data-testid="home-library">{selectedLibraryId || "combined"}</div> }));
vi.mock("./pages/SearchPage", () => ({ default: ({ selectedLibraryId }) => <div data-testid="search-library">{selectedLibraryId || "combined"}</div> }));

import { fetchMe } from "./api/auth";
import { fetchConfig, fetchLibraries } from "./api/fetchBooks";

describe("Root library selection", () => {
  beforeEach(() => { window.location.hash = "#/"; });

  it("defaults to combined, preserves selection across routes, and tolerates an unavailable list", async () => {
    fetchMe.mockResolvedValue({ authRequired: false, user: null });
    fetchConfig.mockResolvedValue({ numberOfBooks: 20 });
    fetchLibraries
      .mockResolvedValueOnce({ libraries: [{ id: "north", name: "North" }, { id: "south", name: "South" }] })
      .mockResolvedValueOnce({ libraries: [{ id: "north", name: "North" }, { id: "south", name: "South" }] })
      .mockResolvedValueOnce({ libraries: [{ id: "south", name: "South" }] });
    render(<Root />);

    expect(await screen.findByTestId("home-library")).toHaveTextContent("combined");
    const selector = await screen.findByRole("combobox");
    fireEvent.change(selector, { target: { value: "north" } });
    expect(screen.getByTestId("home-library")).toHaveTextContent("north");

    fireEvent.click(screen.getByRole("link", { name: t("nav.catalog") }));
    expect(await screen.findByTestId("search-library")).toHaveTextContent("north");
    expect(screen.getByRole("combobox")).toHaveValue("north");

    fireEvent.click(screen.getByRole("link", { name: t("nav.home") }));
    await waitFor(() => expect(fetchLibraries).toHaveBeenCalledTimes(3));
    expect(screen.getByTestId("home-library")).toHaveTextContent("combined");
    expect(screen.queryByRole("combobox")).toBeNull();
  });

  it("keeps combined mode usable when the protected library endpoint fails", async () => {
    fetchMe.mockResolvedValue({ authRequired: false, user: null });
    fetchConfig.mockResolvedValue({});
    fetchLibraries.mockRejectedValue(new Error("unavailable"));
    render(<Root />);
    expect(await screen.findByTestId("home-library")).toHaveTextContent("combined");
    expect(screen.queryByRole("combobox")).toBeNull();
  });
});
