import { captureFB2Position, restoreFB2Position, matchingFB2Progress } from "./fb2Position";
import { t } from "../i18n";
import { lazy, Suspense, useCallback, useEffect, useRef, useState } from "react";
import { Link, useParams } from "react-router-dom";
import {
  fetchChapter,
  fetchProgress,
  fetchReaderPrefs,
  fetchReadMeta,
  localProgress,
  saveProgress,
  saveFB2Progress,
  saveReaderPrefs,
} from "../api/reader";
import "./ReaderPage.css";

// PDF and EPUB engines are heavy — load them only when such a file is opened.
const PdfReader = lazy(() => import("./PdfReader"));

const FONT_SIZES = [17, 19, 21, 24];
const LINE_HEIGHTS = [1.5, 1.72, 1.95];
const MEASURES = ["540px", "640px", "760px"];
const DEFAULT_PREFS = { fontStep: 1, theme: "paper", font: "serif", lineStep: 1, widthStep: 1 };
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

const ReaderPage = () => {
  const { bookId } = useParams();

  const [meta, setMeta] = useState(null);
  const [loaded, setLoaded] = useState([]); // loaded chapters, in order
  const [current, setCurrent] = useState({ index: 0, ratio: 0 }); // the chapter in view
  const [error, setError] = useState(null);
  const [tocOpen, setTocOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [note, setNote] = useState(null);
  const [prefs, setPrefs] = useState(loadPrefs);
  const [immersive, setImmersive] = useState(false);
  const prefsSynced = useRef(false); // server prefs are authoritative once loaded

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
            }
          });
        } else {
          saveProgress(bookId, index, ratio, overall);
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
    (n) => {
      if (!meta || n < 0 || n >= total) return;
      setTocOpen(false);
      setNote(null);
      const already = pageRef.current?.querySelector(`[data-chapter="${n}"]`);
      if (already) {
        window.scrollTo({ top: already.offsetTop - 60, behavior: "smooth" });
        return;
      }
      fetchChapter(bookId, n)
        .then((ch) => {
          clearTimeout(saveTimer.current);
          restoreTo.current = { index: n, ratio: 0, save: true };
          setLoaded([ch]);
          setCurrent({ index: n, ratio: 0 });
          interacted.current = true;
        })
        .catch(() => {});
    },
    [bookId, meta, total, persist]
  );

  useEffect(() => {
    const onKey = (e) => {
      if (e.target.closest?.("input, textarea")) return;
      if (e.key === "ArrowRight") goTo(current.index + 1);
      if (e.key === "ArrowLeft") goTo(current.index - 1);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [current.index, goTo]);

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

  const overall = total > 0 ? (current.index + current.ratio) / total : 0;
  const lastLoaded = loaded.length ? loaded[loaded.length - 1].index : -1;
  const firstLoaded = loaded.length ? loaded[0].index : 0;

  return (
    <div
      className={`reader reader--${prefs.theme} reader--font-${prefs.font ?? "serif"} ${immersive ? "reader--immersive" : ""}`}
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
            <button
              type="button"
              className={prefs.font !== "sans" ? "is-active" : ""}
              style={{ fontFamily: "georgia, serif" }}
              onClick={() => updatePrefs({ font: "serif" })}
            >
              {t("reader.serif")}
            </button>
            <button
              type="button"
              className={prefs.font === "sans" ? "is-active" : ""}
              onClick={() => updatePrefs({ font: "sans" })}
            >
              {t("reader.sans")}
            </button>
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
          <h3>{t("reader.toc")}</h3>
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
        </nav>
      )}

      <main className="reader__page" ref={pageRef} onClick={onPageTap}>
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
        <div className="reader__progress-fill" style={{ width: `${Math.round(overall * 1000) / 10}%` }} />
        <div className="reader__progress-info">
          <span className="reader__progress-chapter">{chapterLabel(meta, current.index)}</span>
          <span className="reader__progress-pct">{Math.round(overall * 100)}%</span>
        </div>
      </footer>
    </div>
  );
};

export default ReaderPage;
