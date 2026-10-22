import { afterEach, describe, expect, it, vi } from "vitest";
import { saveSettings } from "./manage";

describe("saveSettings", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    localStorage.clear();
  });

  it("uses the shared JSON POST behavior", async () => {
    localStorage.setItem("polka-lang", "en");
    const settings = { "enrich.similar_fantlab": "0" };
    const fetch = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ ok: true }) });
    vi.stubGlobal("fetch", fetch);

    await expect(saveSettings(settings)).resolves.toEqual({ ok: true });
    expect(fetch).toHaveBeenCalledWith("/admin/settings", {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json", "X-Polka-Lang": "en" },
      body: JSON.stringify(settings),
    });
  });
});
