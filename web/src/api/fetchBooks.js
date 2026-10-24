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

const get = (endpoint, params = {}, opts = {}) => api.getJson(`main/getBooks/${endpoint}${buildQuery(params)}`, opts);

export const fetchConfig = () => get("getConfig");

export const fetchLibraries = () => api.getJson("api/v1/libraries");

export const fetchHomeShelves = ({ limit, libraryId } = {}) => get("getHomeShelves", { limit, libraryId });

export const fetchCatalogShelves = ({ limit, count, libraryId } = {}) =>
  get("getCatalogShelves", { limit, count, libraryId });

export const fetchShelfBooks = ({ shelfId, limit, offset, libraryId } = {}) =>
  get("getShelfBooks", { shelfId, limit, offset, libraryId });

export const fetchSearchStats = ({ search, selectedGroupID, libraryId } = {}, opts = {}) => get("getSearchStats", { search, selectedGroupID, libraryId }, opts);

export const fetchSearchTitles = ({ search, selectedGroupID, libraryId } = {}, opts = {}) => get("getSearchTitles", { search, selectedGroupID, libraryId }, opts);

export const fetchSearchAuthors = ({ search, selectedGroupID, libraryId } = {}, opts = {}) => get("getSearchAuthors", { search, selectedGroupID, libraryId }, opts);

export const fetchSearchSeries = ({ search, selectedGroupID, libraryId } = {}, opts = {}) => get("getSearchSeries", { search, selectedGroupID, libraryId }, opts);

export const fetchSearchGenres = ({ search, libraryId } = {}, opts = {}) => get("getSearchGenres", { search, libraryId }, opts);

export const fetchAuthorBooks = ({ selectedItemID, selectedGroupID, limit, offset, libraryId } = {}) =>
  get("getSearchAuthorBooks", { selectedItemID, selectedGroupID, limit, offset, libraryId });

export const fetchSeriesBooks = ({ selectedItemID, selectedGroupID, libraryId } = {}) =>
  get("getSearchSeriesBooks", { selectedItemID, selectedGroupID, libraryId });

export const fetchBookForm = ({ selectedItemID } = {}) => get("getBookForm", { selectedItemID });

export const fetchExternalEnrichment = ({ bookId, title, author, isbn } = {}) =>
  get("getExternalEnrichment", { bookId, title, author, isbn });

export const fetchSimilarBooks = ({ bookId, title, author, libraryId } = {}) =>
  get("getSimilarBooks", { bookId, title, author, libraryId });
