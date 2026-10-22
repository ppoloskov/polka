import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { t } from "../i18n";
import AdminLibraryPage from "./AdminLibraryPage";

vi.mock("../api/manage", () => ({
  fetchAdminLibraries: vi.fn(),
  createLibrary: vi.fn(),
  updateLibrary: vi.fn(),
  startLibraryUpdate: vi.fn(),
  fetchSettings: vi.fn(),
  importInpx: vi.fn(),
  importStatus: vi.fn(),
  saveSettings: vi.fn(),
  testSmtp: vi.fn(),
  uploadBooks: vi.fn(),
}));
vi.mock("../api/collections", () => ({
  deleteCollection: vi.fn(),
  fetchCollections: vi.fn(),
  importCollection: vi.fn(),
  rematchCollection: vi.fn(),
  syncCollectionSource: vi.fn(),
}));

import {
  createLibrary, fetchAdminLibraries, fetchSettings, importInpx, importStatus,
  startLibraryUpdate, updateLibrary,
} from "../api/manage";
import { deleteCollection, fetchCollections } from "../api/collections";

const legacy = { id: "legacy", name: "Legacy", kind: "legacy", root: "/books", sourcePath: "/data/catalog.inpx", enabled: true };

const renderPage = () => render(<MemoryRouter><AdminLibraryPage /></MemoryRouter>);

describe("AdminLibraryPage library management", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    fetchAdminLibraries.mockResolvedValue({ libraries: [legacy] });
    fetchSettings.mockResolvedValue({ enrichment: {}, similar: {}, collectionSources: [], smtp: {}, library: null });
    importStatus.mockResolvedValue({ running: false, phase: "idle" });
    fetchCollections.mockResolvedValue({ collections: [] });
    createLibrary.mockResolvedValue({ library: { id: "named-1", name: "Named", kind: "inpx", root: "/books/named", sourcePath: "/data/named.inpx", enabled: true } });
    updateLibrary.mockImplementation((id, patch) => Promise.resolve({ library: { ...legacy, ...patch } }));
    startLibraryUpdate.mockResolvedValue({ started: true });
    importInpx.mockResolvedValue({ started: true });
    deleteCollection.mockResolvedValue({});
  });

  it("creates a saved library without starting an update", async () => {
    renderPage();
    await screen.findByLabelText(t("admin.libraries.renameLabel", { name: "Legacy" }));
    fireEvent.change(screen.getByLabelText(t("admin.libraries.name")), { target: { value: "Named" } });
    fireEvent.change(screen.getByLabelText(t("admin.libraries.kind")), { target: { value: "inpx" } });
    fireEvent.change(screen.getByLabelText(t("admin.libraries.root")), { target: { value: "/books/named" } });
    fireEvent.change(screen.getByLabelText(t("admin.libraries.sourcePath")), { target: { value: "/data/named.inpx" } });
    fireEvent.click(screen.getByRole("button", { name: t("admin.libraries.create") }));

    await waitFor(() => expect(createLibrary).toHaveBeenCalledWith({
      name: "Named", kind: "inpx", root: "/books/named", sourcePath: "/data/named.inpx",
    }));
    expect(startLibraryUpdate).not.toHaveBeenCalled();
  });

  it("toggles enabled state and polls update diff", async () => {
    importStatus
      .mockResolvedValueOnce({ running: false, phase: "idle" })
      .mockResolvedValue({ running: false, phase: "done", libraryId: "legacy", added: 2, removed: 1, changed: 3, unchanged: 8 });
    renderPage();
    await screen.findByLabelText(t("admin.libraries.renameLabel", { name: "Legacy" }));

    fireEvent.click(screen.getByLabelText(t("admin.libraries.enabled", { name: "Legacy" })));
    await waitFor(() => expect(updateLibrary).toHaveBeenCalledWith("legacy", { enabled: false }));

    fireEvent.click(screen.getByRole("button", { name: t("admin.libraries.update") }));
    await waitFor(() => expect(startLibraryUpdate).toHaveBeenCalledWith("legacy", { allowEmpty: false }));
    expect(await screen.findByText(t("admin.libraries.diff", { added: 2, removed: 1, changed: 3, unchanged: 8 }))).toBeInTheDocument();
  });

  it("shows update errors without replacing the saved library list", async () => {
    startLibraryUpdate.mockRejectedValue(new Error("source unavailable"));
    renderPage();
    await screen.findByLabelText(t("admin.libraries.renameLabel", { name: "Legacy" }));
    fireEvent.click(screen.getByRole("button", { name: t("admin.libraries.update") }));
    expect(await screen.findByText(/source unavailable/)).toBeInTheDocument();
    expect(screen.getByLabelText(t("admin.libraries.renameLabel", { name: "Legacy" }))).toBeInTheDocument();
  });

  it("requires an explicit empty-source choice and confirmation", async () => {
    vi.stubGlobal("confirm", vi.fn(() => true));
    renderPage();
    await screen.findByLabelText(t("admin.libraries.renameLabel", { name: "Legacy" }));
    fireEvent.click(screen.getByLabelText(t("admin.libraries.allowEmpty")));
    fireEvent.click(screen.getByRole("button", { name: t("admin.libraries.update") }));
    await waitFor(() => expect(startLibraryUpdate).toHaveBeenCalledWith("legacy", { allowEmpty: true }));
    expect(confirm).toHaveBeenCalledWith(t("admin.libraries.allowEmptyConfirm", { name: "Legacy" }));
    vi.unstubAllGlobals();
  });

  it("keeps the existing import form and request parameters", async () => {
    renderPage();
    await screen.findByLabelText(t("admin.libraries.renameLabel", { name: "Legacy" }));
    const file = new File(["inpx"], "catalog.inpx", { type: "application/octet-stream" });
    fireEvent.change(document.querySelector('input[type="file"][accept=".inpx"]'), { target: { files: [file] } });
    fireEvent.click(screen.getByRole("button", { name: t("admin.import.go") }));
    await waitFor(() => expect(importInpx).toHaveBeenCalledWith({ file, path: "", replace: false }));
  });

  it("refreshes legacy source availability after the existing import completes", async () => {
    const unsavedLegacy = { ...legacy, sourcePath: "" };
    fetchAdminLibraries
      .mockResolvedValueOnce({ libraries: [unsavedLegacy] })
      .mockResolvedValue({ libraries: [legacy] });
    importStatus
      .mockResolvedValueOnce({ running: false, phase: "idle" })
      .mockResolvedValueOnce({ running: true, phase: "loading", libraryId: "legacy" })
      .mockResolvedValue({ running: false, phase: "done", libraryId: "legacy" });
    renderPage();
    await screen.findByLabelText(t("admin.libraries.renameLabel", { name: "Legacy" }));
    expect(screen.getByRole("button", { name: t("admin.libraries.update") })).toBeDisabled();

    fireEvent.change(screen.getByPlaceholderText(t("admin.import.pathPlaceholder")), { target: { value: "/data/catalog.inpx" } });
    fireEvent.click(screen.getByRole("button", { name: t("admin.import.go") }));
    await waitFor(() => expect(importInpx).toHaveBeenCalledWith({ file: null, path: "/data/catalog.inpx", replace: false }));
    await waitFor(() => expect(fetchAdminLibraries).toHaveBeenCalledTimes(2), { timeout: 4000 });
    expect(screen.getByRole("button", { name: t("admin.libraries.update") })).toBeEnabled();
    expect(screen.getByText("/data/catalog.inpx")).toBeInTheDocument();
  });

  it("refreshes the legacy source when the first status poll sees a fast completed import", async () => {
    fetchAdminLibraries
      .mockResolvedValueOnce({ libraries: [{ ...legacy, sourcePath: "" }] })
      .mockResolvedValue({ libraries: [legacy] });
    importStatus
      .mockResolvedValueOnce({ running: false, phase: "idle" })
      .mockResolvedValue({ running: false, phase: "done", libraryId: "legacy" });
    renderPage();
    await screen.findByLabelText(t("admin.libraries.renameLabel", { name: "Legacy" }));

    fireEvent.change(screen.getByPlaceholderText(t("admin.import.pathPlaceholder")), { target: { value: "/data/catalog.inpx" } });
    fireEvent.click(screen.getByRole("button", { name: t("admin.import.go") }));
    await waitFor(() => expect(fetchAdminLibraries).toHaveBeenCalledTimes(2));
    expect(screen.getByRole("button", { name: t("admin.libraries.update") })).toBeEnabled();
  });
});
