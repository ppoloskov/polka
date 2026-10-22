import { t } from "./i18n";
import { useCallback, useEffect, useState } from "react";
import { HashRouter, Navigate, Route, Routes } from "react-router-dom";
import TopBar from "./components/TopBar";
import HomePage from "./pages/HomePage";
import BookPage from "./pages/BookPage";
import BookListPage from "./pages/BookListPage";
import SearchPage from "./pages/SearchPage";
import LoginPage from "./pages/LoginPage";
import AdminUsersPage from "./pages/AdminUsersPage";
import AdminLibraryPage from "./pages/AdminLibraryPage";
import ReaderPage from "./pages/ReaderPage";
import ListsPage from "./pages/ListsPage";
import SyncPage from "./pages/SyncPage";
import { fetchConfig, fetchLibraries } from "./api/fetchBooks";
import { fetchMe, logout } from "./api/auth";
import "./theme.css";
import "./Root.css";

const Root = () => {
  const [config, setConfig] = useState(null);
  const [user, setUser] = useState(null);
  const [authRequired, setAuthRequired] = useState(false);
  const [isDesktop, setIsDesktop] = useState(false);
  const [sync, setSync] = useState(null);
  const [authChecked, setAuthChecked] = useState(false);
  const [version, setVersion] = useState("");
  const [libraries, setLibraries] = useState([]);
  const [selectedLibraryId, setSelectedLibraryId] = useState("");

  const loadConfig = useCallback(() => {
    fetchConfig()
      .then(setConfig)
      .catch(() => setConfig(null));
  }, []);

  useEffect(() => {
    fetchMe()
      .then((res) => {
        setUser(res.user ?? null);
        setAuthRequired(Boolean(res.authRequired));
        setIsDesktop(Boolean(res.desktop));
        setSync(res.sync ?? null);
        setVersion(res.version ?? "");
      })
      .catch(() => {
        setUser(null);
        setAuthRequired(false);
      })
      .finally(() => setAuthChecked(true));
  }, []);

  useEffect(() => {
    if (!authChecked) return;
    if (!authRequired || user) loadConfig();
  }, [authChecked, authRequired, user, loadConfig]);

  const refreshLibraries = useCallback(() => {
    fetchLibraries()
      .then((res) => {
        const available = res?.libraries ?? [];
        setLibraries(available);
        setSelectedLibraryId((id) => available.length > 1 && available.some((item) => item.id === id) ? id : "");
      })
      .catch(() => {
        setLibraries([]);
        setSelectedLibraryId("");
      });
  }, []);

  const handleLogout = () => {
    logout().finally(() => {
      setUser(null);
      setConfig(null);
    });
  };

  if (!authChecked) {
    return <div style={{ padding: 48, textAlign: "center" }}>{t("loading")}</div>;
  }

  if (authRequired && !user) {
    return <LoginPage onLogin={setUser} />;
  }

  const layout = (children) => (
    <>
      <TopBar
        user={user}
        desktop={isDesktop}
        sync={sync}
        libraries={libraries}
        selectedLibraryId={selectedLibraryId}
        onLibraryChange={setSelectedLibraryId}
        refreshLibraries={refreshLibraries}
        onLogout={isDesktop ? undefined : handleLogout}
      />
      <main className="root-main">{children}</main>
      <footer className="root-footer">
        <div className="container">
          {t("footer")}
          {version && <span className="root-footer__version"> · {version}</span>}
        </div>
      </footer>
    </>
  );

  return (
    <HashRouter>
      <Routes>
        <Route path="/" element={layout(<HomePage config={config} selectedLibraryId={selectedLibraryId} />)} />
        <Route path="/search" element={layout(<SearchPage config={config} selectedLibraryId={selectedLibraryId} />)} />
        <Route path="/lists" element={user ? layout(<ListsPage />) : <Navigate to="/" replace />} />
        <Route path="/book/:bookId" element={layout(<BookPage user={user} sync={sync} selectedLibraryId={selectedLibraryId} />)} />
        {(sync || isDesktop) && <Route path="/sync" element={layout(<SyncPage />)} />}
        {/* Читалка — без общей шапки, у неё свой минимальный интерфейс */}
        <Route path="/read/:bookId" element={<ReaderPage />} />
        <Route path="/author/:authorId" element={layout(<BookListPage key={selectedLibraryId} kind="author" selectedLibraryId={selectedLibraryId} />)} />
        <Route path="/series/:seriesId" element={layout(<BookListPage key={selectedLibraryId} kind="series" selectedLibraryId={selectedLibraryId} />)} />
        <Route path="/shelf/:shelfId" element={layout(<BookListPage key={selectedLibraryId} kind="shelf" selectedLibraryId={selectedLibraryId} />)} />
        <Route
          path="/admin/users"
          element={
            user?.role === "admin" && !isDesktop
              ? layout(<AdminUsersPage me={user} />)
              : <Navigate to="/" replace />
          }
        />
        <Route
          path="/admin/library"
          element={
            user?.role === "admin" ? layout(<AdminLibraryPage />) : <Navigate to="/" replace />
          }
        />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </HashRouter>
  );
};

export default Root;
