import { captureFB2Position, restoreFB2Position, matchingFB2Progress } from "./fb2Position";
import { t } from "../i18n";
import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useParams } from "react-router-dom";
import {
  fetchChapter,
  fetchProgress,
  fetchReaderPrefs,
  fetchReadMeta,
  fetchReadSearch,
  fetchBookmarks,
  addBookmark as apiAddBookmark,
  deleteBookmark as apiDeleteBookmark,
  localProgress,
  saveProgress,
  saveFB2Progress,
  saveReaderPrefs,
} from "../api/reader";
import "../readerFonts";
import "./ReaderPage.css";

// PDF and EPUB engines are heavy — load them only when such a file is opened.
const PdfReader = lazy(() => import("./PdfReader"));

const FONT_SIZES = [17, 19, 21, 24];
const LINE_HEIGHTS = [1.5, 1.72, 1.95];
const MEASURES = ["540px", "640px", "760px"];
const DEFAULT_PREFS = {
  fontStep: 1,
  theme: "paper",
  font: "serif",
  lineStep: 1,
  widthStep: 1,
  align: "left",
  hyphens: false,
  indent: false,
  spaceStep: 1,
};
const BOOK_FONTS = () => [
  { id: "serif", label: t("reader.serif"), css: "georgia, serif" },
  { id: "literata", label: "Literata", css: "'Literata', georgia, serif" },
  { id: "ptserif", label: "PT Serif", css: "'PT Serif', georgia, serif" },
  { id: "noto", label: "Noto Serif", css: "'Noto Serif', georgia, serif" },
  { id: "sans", label: t("reader.sans"), css: "" },
];
const THEMES = () => [
  { id: "paper", label: t("reader.theme.paper") },
  { id: "sepia", label: t("reader.theme.sepia") },
  { id: "night", label: t("reader.theme.night") },
];

const loadPrefs = () => {
  try {
    return { ...DEFAULT_PREFS, ...JSON.parse(localStorage.getItem("polka-reader-prefs")) };
  } catch {
    return { ...DEFAULT_PREFS };
  }
};

const chapterLabel = (meta, index) =>
  meta?.chapters?.[index]?.title || (index === 0 ? t("reader.start") : t("reader.chapterN", { n: index + 1 }));

// highlightChapter wraps case-insensitive occurrences of query in the
// chapter DOM with <mark class="reader__hl">; returns the match count.
// The chapter elements come from dangerouslySetInnerHTML, so direct DOM
// edits are safe between renders; re-runs are idempotent (marked nodes are
// skipped by the walker).
const highlightChapter = (el, query) => {
  const lower = query.toLowerCase();
  if (!el || !lower) return 0;
  const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT, {
    acceptNode: (n) =>
      n.parentElement?.closest("mark.reader__hl")
        ? NodeFilter.FILTER_REJECT
        : n.nodeValue.toLowerCase().includes(lower)
          ? NodeFilter.FILTER_ACCEPT
          : NodeFilter.FILTER_REJECT,
  });
  const nodes = [];
  while (walker.nextNode()) nodes.push(walker.currentNode);
  let count = 0;
  for (const node of nodes) {
    const text = node.nodeValue;
    const low = text.toLowerCase();
    const frag = document.createDocumentFragment();
    let pos = 0;
    let at = low.indexOf(lower);
    while (at >= 0) {
      count++;
      if (at > pos) frag.appendChild(document.createTextNode(text.slice(pos, at)));
      const mark = document.createElement("mark");
      mark.className = "reader__hl";
      mark.textContent = text.slice(at, at + lower.length);
      frag.appendChild(mark);
      pos = at + lower.length;
      at = low.indexOf(lower, pos);
    }
    frag.appendChild(document.createTextNode(text.slice(pos)));
    node.parentNode.replaceChild(frag, node);
  }
  return count;
};

const unwrapMarks = (el) => {
  for (const mark of el?.querySelectorAll("mark.reader__hl") ?? []) {
    mark.replaceWith(document.createTextNode(mark.textContent));
  }
};

const ReaderPage = () => {
  const { bookId } = useParams();

  const [meta, setMeta] = useState(null);
  const [loaded, setLoaded] = useState([]); // loaded chapters, in order
  const [current, setCurrent] = useState({ index: 0, ratio: 0 }); // the chapter in view
  const [error, setError] = useState(null);
  const [saveFailed, setSaveFailed] = useState(false);
  const [tocOpen, setTocOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [note, setNote] = useState(null);
  const [prefs, setPrefs] = useState(loadPrefs);
  const [immersive, setImmersive] = useState(false);
  const prefsSynced = useRef(false); // server prefs are authoritative once loaded

  // In-book search and bookmarks (the side panel).
  const [panelTab, setPanelTab] = useState("chapters"); // chapters | bookmarks
  const [panelSearch, setPanelSearch] = useState("");
  const [panelSearchResults, setPanelSearchResults] = useState(null); // matches for panelSearch
  const [panelSearching, setPanelSearching] = useState(false);
  const [highlightQuery, setHighlightQuery] = useState(""); // committed query highlighted in loaded chapters
  const [highlightCount, setHighlightCount] = useState(0);
  const panelSearchTimer = useRef(null);
  const [bookmarks, setBookmarks] = useState([]);

  // Seekable progress bar; null while not dragging.
  const [seekDraft, setSeekDraft] = useState(null);

  const [remoteProgress, setRemoteProgress] = useState(null);
  const revision = useRef(0);
  const digest = useRef("");
  const restoring = useRef(true);
  const interacted = useRef(false);
  const latestPosition = useRef(null);
  const saveQueue = useRef(Promise.resolve());
  const syncBlocked = useRef(false);
  const saveGeneration = useRef(0);
  const serverStored = useRef(false);
  const restoreTo = useRef(null); // {index, ratio} to jump to after render
  const appending = useRef(false);
  const saveTimer = useRef(null);
  const pageRef = useRef(null);
  const sentinelRef = useRef(null);

  const total = meta?.chapters?.length ?? 0;

  // Server-side prefs override the local copy (they follow the user
  // between devices); public mode falls back to localStorage silently.
  useEffect(() => {
    let cancelled = false;
    fetchReaderPrefs()
      .then((server) => {
        if (cancelled) return;
        prefsSynced.current = true;
        if (server && Object.keys(server).length) {
          setPrefs((prev) => ({ ...prev, ...server }));
        }
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, []);

  const totalRef = useRef(0);
  const persist = useCallback(
    (index, ratio) => {
      const overall = totalRef.current > 0 ? Math.min(1, (index + ratio) / totalRef.current) : 0;
      if (serverStored.current) {
        if (digest.current) {
          if (restoring.current || !interacted.current || syncBlocked.current) return;
          const locator = captureFB2Position(pageRef.current);
          if (!locator) return;
          const position = {
            chapter: index,
            position: ratio,
            progress: overall,
            locator,
          };
          const fileDigest = digest.current;
          const generation = saveGeneration.current;
          saveQueue.current = saveQueue.current.then(async () => {
            if (syncBlocked.current || fileDigest !== digest.current || generation !== saveGeneration.current) return;
            try {
              const saved = await saveFB2Progress(bookId, position, revision.current, fileDigest);
              if (fileDigest === digest.current && generation === saveGeneration.current)
                revision.current = saved.revision;
              setSaveFailed(false);
            } catch (err) {
              if (err.status === 412) {
                syncBlocked.current = true;
                setError(err);
              }
              if (err.status === 409 && fileDigest === digest.current && generation === saveGeneration.current) {
                syncBlocked.current = true;
                saveGeneration.current++;
                const remote = await fetchProgress(bookId).catch(() => null);
                if (fileDigest !== digest.current) return;
                setRemoteProgress(remote ?? { revision: revision.current });
              }
              // Unhandled server/network failures must be visible: the
              // reader keeps working, but the user learns the position was
              // not saved instead of leaving silently.
              setSaveFailed(true);
            }
          });
        } else {
          Promise.resolve(saveProgress(bookId, index, ratio, overall))
            .then(() => setSaveFailed(false))
            .catch(() => setSaveFailed(true));
        }
      } else {
        localProgress.set(bookId, index, ratio);
      }
    },
    [bookId]
  );

  // --- Startup: metadata + saved position ---
  useEffect(() => {
    let cancelled = false;
    restoring.current = true;
    interacted.current = false;
    digest.current = "";
    saveGeneration.current++;
    syncBlocked.current = false;
    latestPosition.current = null;
    setRemoteProgress(null);
    Promise.all([fetchReadMeta(bookId), fetchProgress(bookId).catch(() => null)])
      .then(([m, prog]) => {
        if (cancelled) return;
        if (m.format === "pdf") {
          setMeta(m); // a dedicated engine will render it
          return undefined;
        }
        serverStored.current = Boolean(prog?.stored);
        revision.current = prog?.revision ?? 0;
        digest.current = m.format === "fb2" ? (m.digest ?? "") : "";
        const saved = matchingFB2Progress(prog?.stored ? prog : localProgress.get(bookId), digest.current);
        const index = Math.max(0, Math.min(Number(saved?.chapter ?? 0), m.chapters.length - 1));
        restoreTo.current = {
          index,
          ratio: Number(saved?.position ?? 0),
          locator: saved?.locator,
        };
        totalRef.current = m.chapters.length;
        setMeta(m);
        return fetchChapter(bookId, index).then((ch) => {
          if (!cancelled) {
            setLoaded([ch]);
            setCurrent({ index, ratio: restoreTo.current.ratio });
          }
        });
      })
      .catch((err) => !cancelled && setError(err));
    return () => {
      cancelled = true;
    };
  }, [bookId]);

  // --- Jump to the saved position after the chapter renders ---
  useEffect(() => {
    if (!restoreTo.current || loaded.length === 0) return;
    const { index, ratio, locator, save } = restoreTo.current;
    restoring.current = true;
    restoreTo.current = null;
    requestAnimationFrame(() => {
      const el = pageRef.current?.querySelector(`[data-chapter="${index}"]`);
      if (!el) {
        restoring.current = false;
        return;
      }
      if (!restoreFB2Position(el, locator)) {
        window.scrollTo(0, ratio > 0 ? el.offsetTop + ratio * el.offsetHeight - 80 : 0);
      }
      requestAnimationFrame(() => {
        restoring.current = false;
        if (save) persist(index, ratio);
      });
    });
  }, [loaded, persist]);

  // --- Auto-load the next chapter ---
  const appendNext = useCallback(() => {
    if (appending.current || !meta || loaded.length === 0) return;
    const last = loaded[loaded.length - 1].index;
    if (last >= total - 1) return;
    appending.current = true;
    fetchChapter(bookId, last + 1)
      .then((ch) => setLoaded((prev) => (prev.length && prev[prev.length - 1].index === last ? [...prev, ch] : prev)))
      .catch(() => {})
      .finally(() => {
        appending.current = false;
      });
  }, [bookId, meta, loaded, total]);

  useEffect(() => {
    const sentinel = sentinelRef.current;
    if (!sentinel) return undefined;
    const io = new IntersectionObserver(
      (entries) => entries.some((e) => e.isIntersecting) && appendNext(),
      { rootMargin: "1400px 0px" }
    );
    io.observe(sentinel);
    return () => io.disconnect();
  }, [appendNext]);

  // --- Current chapter and read fraction (anchor at a third of the screen) ---
  useEffect(() => {
    if (loaded.length === 0) return undefined;
    let raf = 0;
    const onScroll = () => {
      cancelAnimationFrame(raf);
      raf = requestAnimationFrame(() => {
        const anchor = window.scrollY + window.innerHeight * 0.35;
        const wrappers = pageRef.current?.querySelectorAll("[data-chapter]") ?? [];
        let found = null;
        for (const el of wrappers) {
          if (el.offsetTop <= anchor) found = el;
        }
        if (!found) return;
        const index = Number(found.dataset.chapter);
        const ratio = Math.max(0, Math.min(1, (anchor - found.offsetTop) / Math.max(1, found.offsetHeight)));
        latestPosition.current = { index, ratio };
        setCurrent((prev) => (prev.index !== index || Math.abs(prev.ratio - ratio) > 0.005 ? { index, ratio } : prev));
        clearTimeout(saveTimer.current);
        saveTimer.current = setTimeout(() => persist(index, ratio), 1200);
      });
    };
    onScroll();
    window.addEventListener("scroll", onScroll, { passive: true });
    const flush = () => {
      const position = latestPosition.current;
      if (document.visibilityState === "hidden" && position?.index != null) persist(position.index, position.ratio);
    };
    document.addEventListener("visibilitychange", flush);
    return () => {
      window.removeEventListener("scroll", onScroll);
      document.removeEventListener("visibilitychange", flush);
      cancelAnimationFrame(raf);
      clearTimeout(saveTimer.current);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loaded.length, persist]);

  // Restoring/laying out a chapter must not publish a new reading position.
  useEffect(() => {
    let cancelled = false;
    const markInteraction = () => {
      if (!restoring.current) interacted.current = true;
    };
    for (const event of ["wheel", "touchstart", "keydown", "pointerdown"])
      window.addEventListener(event, markInteraction, { passive: true });
    const checkRemote = async () => {
      if (document.visibilityState === "hidden" || !digest.current || !serverStored.current) return;
      await saveQueue.current;
      const remote = await fetchProgress(bookId).catch(() => null);
      if (!cancelled && remote && remote.revision !== revision.current) {
        syncBlocked.current = true;
        saveGeneration.current++;
        clearTimeout(saveTimer.current);
        setRemoteProgress(remote);
      }
    };
    window.addEventListener("focus", checkRemote);
    document.addEventListener("visibilitychange", checkRemote);
    return () => {
      cancelled = true;
      for (const event of ["wheel", "touchstart", "keydown", "pointerdown"])
        window.removeEventListener(event, markInteraction);
      window.removeEventListener("focus", checkRemote);
      document.removeEventListener("visibilitychange", checkRemote);
    };
  }, [bookId]);

  const continueRemote = async () => {
    try {
      const fetched = await fetchProgress(bookId);
      const remote = matchingFB2Progress(fetched, digest.current) ?? { revision: fetched.revision };
      const index = Math.max(0, Math.min(remote.chapter ?? 0, total - 1));
      restoring.current = true;
      interacted.current = false;
      clearTimeout(saveTimer.current);
      const chapter = await fetchChapter(bookId, index);
      revision.current = remote.revision;
      restoreTo.current = { index, ratio: remote.position ?? 0, locator: remote.locator };
      setLoaded([chapter]);
      setCurrent({ index, ratio: remote.position ?? 0 });
      syncBlocked.current = false;
      setRemoteProgress(null);
    } catch {
      restoring.current = false;
    }
  };

  // --- Navigate to a chapter (TOC, keyboard) ---
  const goTo = useCallback(
    (n, ratio = 0, locator = "") => {
      if (!meta || n < 0 || n >= total) return;
      setTocOpen(false);
      setNote(null);
      const already = pageRef.current?.querySelector(`[data-chapter="${n}"]`);
      if (already) {
        // An exact locator (bookmark/progress) wins over the scroll fraction.
        if (locator && restoreFB2Position(already, locator)) {
          setCurrent({ index: n, ratio });
          return;
        }
        const top = already.offsetTop + (ratio > 0 ? ratio * already.offsetHeight - 60 : -60);
        window.scrollTo({ top: Math.max(0, top), behavior: "smooth" });
        setCurrent({ index: n, ratio });
        return;
      }
      fetchChapter(bookId, n)
        .then((ch) => {
          clearTimeout(saveTimer.current);
          restoreTo.current = { index: n, ratio, locator, save: true };
          setLoaded([ch]);
          setCurrent({ index: n, ratio });
          interacted.current = true;
        })
        .catch(() => {});
    },
    [bookId, meta, total, persist]
  );

  // --- Side panel: in-book search and bookmarks ---

  const refreshBookmarks = useCallback(() => {
    if (serverStored.current) {
      fetchBookmarks(bookId)
        .then((list) => setBookmarks(list ?? []))
        .catch(() => setBookmarks([]));
    } else {
      try {
        setBookmarks(JSON.parse(localStorage.getItem(`polka-bookmarks:${bookId}`) ?? "[]"));
      } catch {
        setBookmarks([]);
      }
    }
  }, [bookId]);

  // Public mode keeps bookmarks in localStorage (same fallback as progress).
  useEffect(() => {
    if (tocOpen) refreshBookmarks();
  }, [tocOpen, refreshBookmarks]);

  const addHereBookmark = () => {
    const entry = {
      chapter: current.index,
      position: current.ratio,
      locator: captureFB2Position(pageRef.current) || "",
      label: chapterLabel(meta, current.index),
    };
    if (serverStored.current) {
      apiAddBookmark(bookId, entry)
        .then((saved) => {
          setBookmarks((prev) =>
            [...prev.filter((b) => b.id !== saved.id), saved].sort((a, b) => a.chapter - b.chapter || a.position - b.position),
          );
        })
        .catch(() => {});
    } else {
      const next = [...bookmarks, { ...entry, id: Date.now() }].sort(
        (a, b) => a.chapter - b.chapter || a.position - b.position,
      );
      setBookmarks(next);
      try {
        localStorage.setItem(`polka-bookmarks:${bookId}`, JSON.stringify(next));
      } catch {
        /* ignore */
      }
    }
  };

  const removeBookmark = (id) => {
    if (serverStored.current) apiDeleteBookmark(bookId, id).catch(() => {});
    const next = bookmarks.filter((b) => b.id !== id);
    setBookmarks(next);
    if (!serverStored.current) {
      try {
        localStorage.setItem(`polka-bookmarks:${bookId}`, JSON.stringify(next));
      } catch {
        /* ignore */
      }
    }
  };

  const commitPanelSearch = useCallback(
    (q) => {
      q = q.trim();
      if (q.length < 2) {
        setPanelSearchResults(null);
        setPanelSearching(false);
        return;
      }
      setPanelSearching(true);
      fetchReadSearch(bookId, q)
        .then((res) => setPanelSearchResults(res?.matches ?? []))
        .catch(() => setPanelSearchResults([]))
        .finally(() => setPanelSearching(false));
    },
    [bookId],
  );

  const onPanelSearch = (e) => {
    const q = e.target.value;
    setPanelSearch(q);
    clearTimeout(panelSearchTimer.current);
    panelSearchTimer.current = setTimeout(() => commitPanelSearch(q), 350);
  };

  // Clearing the panel query un-highlights the rendered chapters.
  useEffect(() => {
    if (panelSearch.trim().length < 2 && highlightQuery) setHighlightQuery("");
  }, [panelSearch, highlightQuery]);

  // Highlight the committed query in every loaded chapter (fired per load so
  // appended chapters get theirs too; unwrapped whenever the query clears).
  const jumpAndHighlight = (match) => {
    setHighlightQuery(panelSearch.trim());
    goTo(match.index, 0);
  };

  useEffect(() => {
    if (loaded.length === 0) return;
    const q = highlightQuery.trim();
    if (!q) {
      unwrapMarks(pageRef.current);
      setHighlightCount(0);
      return;
    }
    let count = 0;
    for (const el of pageRef.current?.querySelectorAll("[data-chapter]") ?? []) {
      count += highlightChapter(el, q);
    }
    setHighlightCount(count);
    // highlightQuery drives the marks; loaded changes re-run for appends.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loaded, highlightQuery]);

  const commitSeek = () => {
    if (seekDraft == null) return;
    const target = chapterByPosition(seekDraft * bookLength);
    setSeekDraft(null);
    goTo(target.index, target.ratio);
  };

  useEffect(() => {
    const onKey = (e) => {
      if (e.target.closest?.("input, textarea")) return;
      if (e.key === "ArrowRight") goTo(current.index + 1);
      if (e.key === "ArrowLeft") goTo(current.index - 1);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [current.index, goTo]);

  // Illustrations that fail to load are hidden: Flibusta dumps strip image
  // payloads from FB2 files, so some <img> references (also "cover" ids in
  // books absent from the covers sidecar) stay unresolved. The error event
  // does not bubble but does propagate through the capture phase, allowing
  // a single document-level listener to cover dynamically loaded chapters.
  useEffect(() => {
    const onError = (e) => {
      const img = e.target;
      if (img?.tagName === "IMG" && img.closest(".reader__content")) {
        img.classList.add("fb2-img-missing");
      }
    };
    document.addEventListener("error", onError, { capture: true });
    return () => document.removeEventListener("error", onError, { capture: true });
  }, []);

  // --- Footnotes ---
  const onArticleClick = (e) => {
    const jump = e.target.closest("[data-goto]");
    if (jump) {
      e.preventDefault();
      const n = Number(jump.getAttribute("data-goto"));
      if (Number.isInteger(n)) goTo(n);
      return;
    }
    const ref = e.target.closest("[data-note]");
    if (!ref) return;
    e.preventDefault();
    const html = meta?.notes?.[ref.getAttribute("data-note")];
    if (html) setNote({ html });
  };

  // --- Touch: tapping a screen edge flips chapters (touch devices only) ---
  const onPageTap = (e) => {
    if (!window.matchMedia("(pointer: coarse)").matches) return;
    if (e.target.closest("a, button, [data-note], img, input")) return;
    if (window.getSelection()?.toString()) return;
    const x = e.clientX / window.innerWidth;
    if (x < 0.15) {
      goTo(current.index - 1);
    } else if (x > 0.85) {
      goTo(current.index + 1);
    } else {
      // Distraction-free reading: the center tap hides the interface.
      setImmersive((v) => !v);
      setSettingsOpen(false);
      setTocOpen(false);
    }
  };

  const updatePrefs = (patch) => {
    setPrefs((prev) => {
      const next = { ...prev, ...patch };
      try {
        localStorage.setItem("polka-reader-prefs", JSON.stringify(next));
      } catch {
        /* ignore */
      }
      return next;
    });
    if (prefsSynced.current) saveReaderPrefs(patch).catch(() => {});
  };

  const fullscreenAvailable = typeof document !== "undefined" && document.fullscreenEnabled;
  const toggleFullscreen = () => {
    if (document.fullscreenElement) {
      document.exitFullscreen?.();
    } else {
      document.documentElement.requestFullscreen?.().catch(() => {});
    }
  };

  // Chapter sizes from the server (rendered HTML length) give the progress
  // bar and seek a proper, per-chapter-weighted scale; chapters default to
  // equal weights when the sizes are unavailable. Declared before the PDF
  // and error early returns so the hook order is unconditional.
  const chapterSizes = useMemo(
    () => meta?.chapters?.map((c) => (typeof c.size === "number" && c.size > 0 ? c.size : 1000)) ?? [],
    [meta],
  );
  const chapterOffsets = useMemo(() => {
    const out = [0];
    for (const size of chapterSizes) out.push(out[out.length - 1] + size);
    return out;
  }, [chapterSizes]);
  const bookLength = chapterOffsets[chapterOffsets.length - 1] ?? 0;
  const chapterByPosition = useMemo(() => {
    return (pos) => {
      // First chapter whose end passes the absolute position.
      let lo = 0;
      while (lo + 1 < chapterOffsets.length && chapterOffsets[lo + 1] <= pos) lo++;
      const size = chapterSizes[lo] ?? 1;
      return { index: lo, ratio: Math.max(0, Math.min(1, chapterSizes[lo] ? (pos - chapterOffsets[lo]) / size : 0)) };
    };
  }, [chapterOffsets, chapterSizes]);
  const overall = total > 0 ? (chapterOffsets[current.index] + current.ratio * chapterSizes[current.index]) / bookLength : 0;
  const lastLoaded = loaded.length ? loaded[loaded.length - 1].index : -1;
  const firstLoaded = loaded.length ? loaded[0].index : 0;

  if (error) {
    const unsupported = error.status === 415;
    return (
      <div className="reader reader--paper">
        <div className="reader__error">
          <p>{unsupported ? t("reader.unsupported") : t("reader.fail")}</p>
          <Link to={`/book/${bookId}`} className="btn btn-ghost">
            {t("reader.back")}
          </Link>
        </div>
      </div>
    );
  }

  if (meta?.format === "pdf") {
    const Engine = PdfReader;
    return (
      <Suspense fallback={<div style={{ padding: 48, textAlign: "center" }}>{t("loading")}</div>}>
        <Engine bookId={bookId} meta={meta} />
      </Suspense>
    );
  }

  // Chapter sizes block lives above the early returns (unconditional hooks).

  return (
    <div
      className={`reader reader--${prefs.theme} reader--font-${prefs.font ?? "serif"} ${
        prefs.align === "justify" ? "reader--justify" : ""
      } ${prefs.hyphens ? "reader--hyphens" : ""} ${prefs.indent ? "reader--indent" : ""} reader--space-${prefs.spaceStep ?? 1} ${
        immersive ? "reader--immersive" : ""
      }`}
      lang={meta?.lang ?? ""}
      style={{
        "--reader-font-size": `${FONT_SIZES[prefs.fontStep] ?? FONT_SIZES[1]}px`,
        "--reader-line-height": LINE_HEIGHTS[prefs.lineStep] ?? LINE_HEIGHTS[1],
        "--reader-measure": MEASURES[prefs.widthStep] ?? MEASURES[1],
      }}
    >
      <header className="reader__bar">
        <Link to={`/book/${bookId}`} className="reader__bar-btn" title={t("reader.toBook")}>
          ←
        </Link>
        <div className="reader__bar-title">
          <span className="reader__bar-book">{meta?.title ?? "…"}</span>
          {meta?.authors && <span className="reader__bar-author">{meta.authors}</span>}
        </div>
        <button type="button" className="reader__bar-btn" title={t("reader.toc")} onClick={() => setTocOpen((v) => !v)}>
          ☰
        </button>
        <button
          type="button"
          className="reader__bar-btn"
          title={t("reader.bookmarkAdd")}
          onClick={() => {
            addHereBookmark();
            setPanelTab("bookmarks");
            setTocOpen(true);
          }}
        >
          ⚑
        </button>
        <button type="button" className="reader__bar-btn reader__bar-btn--aa" title={t("reader.settings")} onClick={() => setSettingsOpen((v) => !v)}>
          Aa
        </button>
      </header>

      {settingsOpen && (
        <div className="reader__settings">
          <div className="reader__settings-group">
            <span className="reader__settings-label">{t("reader.font")}</span>
            <button type="button" disabled={prefs.fontStep === 0} onClick={() => updatePrefs({ fontStep: prefs.fontStep - 1 })}>
              A−
            </button>
            <button
              type="button"
              disabled={prefs.fontStep === FONT_SIZES.length - 1}
              onClick={() => updatePrefs({ fontStep: prefs.fontStep + 1 })}
            >
              A+
            </button>
          </div>
          <div className="reader__settings-group">
            <span className="reader__settings-label">{t("reader.typeface")}</span>
            {BOOK_FONTS().map((f) => (
              <button
                type="button"
                key={f.id}
                className={(prefs.font ?? "serif") === f.id ? "is-active" : ""}
                style={f.css ? { fontFamily: f.css } : undefined}
                onClick={() => updatePrefs({ font: f.id })}
              >
                {f.label}
              </button>
            ))}
          </div>
          <div className="reader__settings-group">
            <span className="reader__settings-label">{t("reader.align")}</span>
            <button
              type="button"
              className={(prefs.align ?? "left") !== "justify" ? "is-active" : ""}
              onClick={() => updatePrefs({ align: "left" })}
            >
              {t("reader.align.left")}
            </button>
            <button
              type="button"
              className={(prefs.align ?? "left") === "justify" ? "is-active" : ""}
              onClick={() => updatePrefs({ align: "justify" })}
            >
              {t("reader.align.justify")}
            </button>
          </div>
          <div className="reader__settings-group">
            <span className="reader__settings-label">{t("reader.paragraph")}</span>
            <button
              type="button"
              className={prefs.indent ? "is-active" : ""}
              onClick={() => updatePrefs({ indent: !prefs.indent })}
            >
              {t("reader.indent")}
            </button>
            <button
              type="button"
              className={prefs.hyphens ? "is-active" : ""}
              onClick={() => updatePrefs({ hyphens: !prefs.hyphens })}
            >
              {t("reader.hyphens")}
            </button>
            {[
              { step: 0, label: t("reader.space.tight") },
              { step: 1, label: t("reader.space.normal") },
              { step: 2, label: t("reader.space.roomy") },
            ].map((s) => (
              <button
                type="button"
                key={s.step}
                className={(prefs.spaceStep ?? 1) === s.step ? "is-active" : ""}
                onClick={() => updatePrefs({ spaceStep: s.step })}
              >
                {s.label}
              </button>
            ))}
          </div>
          <div className="reader__settings-group">
            <span className="reader__settings-label">{t("reader.lineHeight")}</span>
            {LINE_HEIGHTS.map((lh, i) => (
              <button
                type="button"
                key={lh}
                className={(prefs.lineStep ?? 1) === i ? "is-active" : ""}
                onClick={() => updatePrefs({ lineStep: i })}
              >
                {lh}
              </button>
            ))}
          </div>
          <div className="reader__settings-group">
            <span className="reader__settings-label">{t("reader.measure")}</span>
            {[t("reader.measure.narrow"), t("reader.measure.normal"), t("reader.measure.wide")].map((label, i) => (
              <button
                type="button"
                key={label}
                className={(prefs.widthStep ?? 1) === i ? "is-active" : ""}
                onClick={() => updatePrefs({ widthStep: i })}
              >
                {label}
              </button>
            ))}
          </div>
          <div className="reader__settings-group">
            <span className="reader__settings-label">{t("reader.theme")}</span>
            {THEMES().map((th) => (
              <button
                type="button"
                key={th.id}
                className={prefs.theme === th.id ? "is-active" : ""}
                onClick={() => updatePrefs({ theme: th.id })}
              >
                {th.label}
              </button>
            ))}
          </div>
          {fullscreenAvailable && (
            <div className="reader__settings-group">
              <span className="reader__settings-label">{t("reader.fullscreen")}</span>
              <button type="button" onClick={toggleFullscreen}>
                ⛶
              </button>
            </div>
          )}
          <p className="reader__settings-hint">{t("reader.immersiveHint")}</p>
        </div>
      )}

      {tocOpen && (
        <nav className="reader__toc">
          <input
            type="search"
            className="reader__toc-search"
            placeholder={t("reader.searchInBook")}
            value={panelSearch}
            onChange={onPanelSearch}
            autoComplete="off"
          />
          {panelSearchResults != null && (
            <div className="reader__search-results">
              {panelSearching && <p className="reader__toc-hint">{t("loading")}</p>}
              {!panelSearching && panelSearchResults.length === 0 && (
                <p className="reader__toc-hint">{t("reader.noMatches")}</p>
              )}
              {panelSearchResults.map((m) => (
                <button
                  key={m.index}
                  type="button"
                  className={`reader__search-hit ${m.index === current.index ? "is-active" : ""}`}
                  onClick={() => jumpAndHighlight(m)}
                >
                  <span className="reader__search-title">
                    {m.title} · {t("reader.matches", { n: m.count })}
                  </span>
                  {m.snippet && <span className="reader__search-snippet">{m.snippet}</span>}
                </button>
              ))}
              {highlightCount > 0 && <p className="reader__toc-hint">{t("reader.matchesShown", { n: highlightCount })}</p>}
            </div>
          )}
          {panelSearchResults == null && (
            <>
              <div className="reader__toc-tabs">
                <button type="button" className={panelTab === "chapters" ? "is-active" : ""} onClick={() => setPanelTab("chapters")}>
                  {t("reader.toc")}
                </button>
                <button
                  type="button"
                  className={panelTab === "bookmarks" ? "is-active" : ""}
                  onClick={() => setPanelTab("bookmarks")}
                >
                  {t("reader.bookmarks")}
                </button>
              </div>
              {panelTab === "chapters" ? (
                <ol>
                  {meta?.chapters?.map((ch) => (
                    <li key={ch.index}>
                      <button
                        type="button"
                        className={ch.index === current.index ? "is-active" : ""}
                        onClick={() => goTo(ch.index)}
                      >
                        {ch.title}
                      </button>
                    </li>
                  ))}
                </ol>
              ) : (
                <div className="reader__bookmarks">
                  {!bookmarks.length && <p className="reader__toc-hint">{t("reader.noBookmarks")}</p>}
                  {bookmarks.map((b) => (
                    <div key={b.id} className="reader__bookmark">
                      <button
                        type="button"
                        className="reader__bookmark-jump"
                        onClick={() => goTo(b.chapter, b.position, b.locator ?? "")}
                      >
                        <span className="reader__bookmark-label">{b.label || chapterLabel(meta, b.chapter)}</span>
                        <span className="reader__bookmark-pos">{Math.round((b.position ?? 0) * 100)}%</span>
                      </button>
                      <button
                        type="button"
                        className="reader__bookmark-del"
                        onClick={() => removeBookmark(b.id)}
                        title={t("reader.delete")}
                      >
                        ×
                      </button>
                    </div>
                  ))}
                </div>
              )}
            </>
          )}
        </nav>
      )}

      <main className="reader__page" ref={pageRef} onClick={onPageTap}>
        {saveFailed && (
          <aside role="alert" className="reader__sync-notice reader__save-notice">
            <p>{t("reader.saveFailed")}</p>
          </aside>
        )}
        {firstLoaded > 0 && (
          <button type="button" className="reader__prev-link" onClick={() => goTo(firstLoaded - 1)}>
            ↑ {chapterLabel(meta, firstLoaded - 1)}
          </button>
        )}

        {remoteProgress && (
          <aside role="status" className="reader__sync-notice">
            <p>{t("reader.syncChanged")}</p>
            <button onClick={continueRemote}>{t("reader.syncContinue")}</button>
            <button
              onClick={() => {
                revision.current = remoteProgress.revision;
                syncBlocked.current = false;
                setRemoteProgress(null);
                interacted.current = true;
                const pos = latestPosition.current;
                if (pos?.index != null) persist(pos.index, pos.ratio);
              }}
            >
              {t("reader.syncKeep")}
            </button>
          </aside>
        )}
        {loaded.map((ch) => (
          <section key={ch.index} data-chapter={ch.index} className="reader__chapter">
            <header className="reader__chapter-head">
              <p className="reader__eyebrow">
                {total > 1 ? t("reader.chapterOf", { n: ch.index + 1, total }) : meta?.authors}
              </p>
              {ch.titleHtml ? (
                <h1 className="reader__chapter-title" dangerouslySetInnerHTML={{ __html: ch.titleHtml }} />
              ) : (
                <h1 className="reader__chapter-title">{chapterLabel(meta, ch.index)}</h1>
              )}
            </header>
            <article
              className="reader__content"
              onClick={onArticleClick}
              dangerouslySetInnerHTML={{ __html: ch.html }}
            />
          </section>
        ))}

        {loaded.length === 0 && <div className="reader__loading">{t("loading")}</div>}

        <div ref={sentinelRef} aria-hidden="true" />

        {lastLoaded >= total - 1 && loaded.length > 0 && (
          <div className="reader__fin">
            <span>{t("reader.fin")}</span>
            <Link to={`/book/${bookId}`} className="btn btn-ghost">
              {t("reader.toBook")}
            </Link>
          </div>
        )}
      </main>

      {note && (
        <div className="reader__note" role="dialog">
          <button type="button" className="reader__note-close" onClick={() => setNote(null)} aria-label={t("reader.close")}>
            ×
          </button>
          <div dangerouslySetInnerHTML={{ __html: note.html }} />
        </div>
      )}

      <footer className="reader__progress" aria-label="progress">
        <input
          type="range"
          className="reader__seek"
          min="0"
          max="1"
          step="0.002"
          value={seekDraft ?? overall}
          aria-label={t("reader.position")}
          onChange={(e) => setSeekDraft(parseFloat(e.target.value))}
          onPointerUp={commitSeek}
          onTouchEnd={commitSeek}
          onKeyUp={(e) => (e.key === "Enter" || e.key === " " ? commitSeek() : undefined)}
        />
        <div className="reader__progress-fill" style={{ width: `${Math.round((seekDraft ?? overall) * 1000) / 10}%` }} />
        <div className="reader__progress-info">
          <span className="reader__progress-chapter">{chapterLabel(meta, current.index)}</span>
          <span className="reader__progress-pct">{Math.round((seekDraft ?? overall) * 100)}%</span>
        </div>
      </footer>
    </div>
  );
};

export default ReaderPage;
