import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { t } from "../i18n";
import TopBar from "./TopBar";

describe("TopBar catalog library selector", () => {
  it("renders on catalog routes and reports a selection", () => {
    const onLibraryChange = vi.fn();
    render(
      <MemoryRouter initialEntries={["/search?q=space"]}>
        <TopBar libraries={[{ id: "north", name: "North" }, { id: "south", name: "South" }]} selectedLibraryId="" onLibraryChange={onLibraryChange} />
      </MemoryRouter>
    );
    const selector = screen.getByRole("combobox");
    fireEvent.change(selector, { target: { value: "north" } });
    expect(onLibraryChange).toHaveBeenCalledWith("north");
  });

  it("hides the selector outside catalog routes", () => {
    render(
      <MemoryRouter initialEntries={["/admin/library"]}>
        <TopBar libraries={[{ id: "north", name: "North" }]} selectedLibraryId="" onLibraryChange={vi.fn()} />
      </MemoryRouter>
    );
    expect(screen.queryByRole("combobox")).toBeNull();
  });

  it("hides the selector when only one enabled library exists", () => {
    render(
      <MemoryRouter initialEntries={["/"]}>
        <TopBar libraries={[{ id: "north", name: "North" }]} />
      </MemoryRouter>
    );
    expect(screen.queryByRole("combobox")).toBeNull();
  });

  it("refreshes the library list when returning to catalog routes", async () => {
    const refreshLibraries = vi.fn();
    render(
      <MemoryRouter initialEntries={["/admin/library"]}>
        <TopBar libraries={[{ id: "north", name: "North" }, { id: "south", name: "South" }]} refreshLibraries={refreshLibraries} />
      </MemoryRouter>
    );
    expect(refreshLibraries).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("link", { name: t("nav.home") }));
    await waitFor(() => expect(refreshLibraries).toHaveBeenCalledTimes(1));
  });
});
