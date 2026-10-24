import { getLang } from "../i18n";
import api from "./api";

export const fetchReadMeta = (bookId) => api.getJson(`api/v1/read/${bookId}`);

export const fetchChapter = (bookId, n) => api.getJson(`api/v1/read/${bookId}/chapter/${n}`);

export const fetchProgress = (bookId) => api.getJson(`api/v1/read/${bookId}/progress`);

export const saveProgress = async (bookId, chapter, position, progress, locator = "") => {
  const res = await fetch(api.buildUrl(`api/v1/read/${bookId}/progress`), {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json", "X-Polka-Lang": getLang() },
    body: JSON.stringify({ chapter, position, progress, locator }),
  });
  if (!res.ok) {
    const err = new Error(`Request failed: ${res.status}`);
    err.status = res.status;
    throw err;
  }
  return res.json();
};

// Fallback when the server does not store the position (public mode, no login).
export const localProgress = {
  get(bookId) {
    try {
      return JSON.parse(localStorage.getItem(`polka-read-${bookId}`));
    } catch {
      return null;
    }
  },
  set(bookId, chapter, position) {
    try {
      localStorage.setItem(`polka-read-${bookId}`, JSON.stringify({ chapter, position }));
    } catch {
      /* quota exceeded — no big deal */
    }
  },
};

// Per-user reader preferences on the server (401 in public mode is fine —
// the caller falls back to localStorage).
export const fetchReaderPrefs = () => api.getJson("api/v1/me/reader-prefs");

export const saveReaderPrefs = (prefs) =>
  fetch(api.buildUrl("api/v1/me/reader-prefs"), {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(prefs),
  });

// FB2 saves carry a revision and file identity. Conflicts must reach the UI.
export const saveFB2Progress = async (bookId, position, revision, digest) => {
  const response = await fetch(api.buildUrl(`api/v1/read/${bookId}/progress`), {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ ...position, revision, digest }),
  });
  if (!response.ok) {
    const error = new Error(await response.text());
    error.status = response.status;
    throw error;
  }
  return response.json();
};

// In-book search: server scans the parsed book and returns matching
// chapters with occurrence counts and one snippet.
export const fetchReadSearch = async (bookId, q) => {
  const res = await fetch(api.buildUrl(`api/v1/read/${bookId}/search?q=${encodeURIComponent(q)}`), {
    credentials: "include",
    headers: { "X-Polka-Lang": getLang() },
  });
  if (!res.ok) {
    const err = new Error(`Request failed: ${res.status}`);
    err.status = res.status;
    throw err;
  }
  return res.json();
};

const readBookFetch = async (path, init = {}) => {
  const res = await fetch(api.buildUrl(path), {
    credentials: "include",
    headers: { "Content-Type": "application/json", "X-Polka-Lang": getLang() },
    ...init,
  });
  if (!res.ok) {
    const err = new Error(`Request failed: ${res.status}`);
    err.status = res.status;
    throw err;
  }
  return res.json();
};

// Reading bookmarks for one book. Server-side for logged-in users; the
// public mode falls back to localStorage in the reader component.
export const fetchBookmarks = async (bookId) => {
  const res = await readBookFetch(`api/v1/read/${bookId}/bookmarks`);
  return res?.bookmarks ?? [];
};

export const addBookmark = (bookId, bookmark) =>
  readBookFetch(`api/v1/read/${bookId}/bookmarks`, { method: "POST", body: JSON.stringify(bookmark) }).then(
    (res) => res.bookmark,
  );

export const deleteBookmark = (bookId, id) =>
  readBookFetch(`api/v1/read/${bookId}/bookmarks?id=${id}`, { method: "DELETE" });
