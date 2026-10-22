import api from "./api";

const buildQuery = (params) => {
  const usp = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null || value === "") continue;
    usp.set(key, String(value));
  }
  const qs = usp.toString();
  return qs ? `?${qs}` : "";
};

const get = (endpoint, params = {}) => api.getJson(`main/getBooks/${endpoint}${buildQuery(params)}`);

export const fetchConfig = () => get("getConfig");

export const fetchLibraries = () => api.getJson("api/v1/libraries");

export const fetchHomeShelves = ({ limit, libraryId } = {}) => get("getHomeShelves", { limit, libraryId });

export const fetchCatalogShelves = ({ limit, count, libraryId } = {}) =>
  get("getCatalogShelves", { limit, count, libraryId });

export const fetchShelfBooks = ({ shelfId, limit, offset, libraryId } = {}) =>
  get("getShelfBooks", { shelfId, limit, offset, libraryId });

export const fetchSearchStats = ({ search, selectedGroupID, libraryId } = {}) => get("getSearchStats", { search, selectedGroupID, libraryId });

export const fetchSearchTitles = ({ search, selectedGroupID, libraryId } = {}) => get("getSearchTitles", { search, selectedGroupID, libraryId });

export const fetchSearchAuthors = ({ search, selectedGroupID, libraryId } = {}) => get("getSearchAuthors", { search, selectedGroupID, libraryId });

export const fetchSearchSeries = ({ search, selectedGroupID, libraryId } = {}) => get("getSearchSeries", { search, selectedGroupID, libraryId });

export const fetchSearchGenres = ({ search, libraryId } = {}) => get("getSearchGenres", { search, libraryId });

export const fetchAuthorBooks = ({ selectedItemID, selectedGroupID, libraryId } = {}) =>
  get("getSearchAuthorBooks", { selectedItemID, selectedGroupID, libraryId });

export const fetchSeriesBooks = ({ selectedItemID, selectedGroupID, libraryId } = {}) =>
  get("getSearchSeriesBooks", { selectedItemID, selectedGroupID, libraryId });

export const fetchBookForm = ({ selectedItemID } = {}) => get("getBookForm", { selectedItemID });

export const fetchExternalEnrichment = ({ bookId, title, author, isbn } = {}) =>
  get("getExternalEnrichment", { bookId, title, author, isbn });

export const fetchSimilarBooks = ({ bookId, title, author, libraryId } = {}) =>
  get("getSimilarBooks", { bookId, title, author, libraryId });
