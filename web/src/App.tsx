import { lazy, Suspense, useEffect, useState } from "react";
import {
  Activity,
  ArrowUpRight,
  BookOpen,
  ChevronRight,
  CircleHelp,
  Hash,
  Languages,
  LayoutDashboard,
  Menu,
  Monitor,
  Moon,
  Sun,
  Network,
  RefreshCw,
  Search,
  X,
} from "lucide-react";
import { useQuery } from "./api";
import { Empty, QueryView } from "./components";
import { useI18n, useT, type Dictionary, type Locale } from "./i18n";
import { filtersOnly, Link, routeURL, useRouter } from "./router";
import type { Meta, Page } from "./types";
import { useTheme, type ThemeMode } from "./theme";
import {
  ActivityView,
  EntitiesView,
  LibraryView,
  OverviewView,
  ReaderView,
} from "./Views";

const GraphView = lazy(() =>
  import("./GraphView").then((module) => ({ default: module.GraphView })),
);

const navigation = [
  { page: "overview", label: "nav.overview", icon: LayoutDashboard },
  { page: "library", label: "nav.library", icon: BookOpen },
  { page: "graph", label: "nav.graph", icon: Network },
  { page: "entities", label: "nav.entities", icon: Hash },
  { page: "activity", label: "nav.activity", icon: Activity },
] as const satisfies ReadonlyArray<{
  page: Page;
  label: keyof Dictionary;
  icon: typeof LayoutDashboard;
}>;
export function App() {
  const meta = useQuery<Meta>("/api/meta");
  return (
    <QueryView query={meta}>{(data) => <Workspace meta={data} />}</QueryView>
  );
}
function Workspace({ meta }: { meta: Meta }) {
  const { route, navigate } = useRouter();
  const t = useT();
  const stores = useQuery<{ stores: string[] }>("/api/stores");
  const [refresh, setRefresh] = useState(0);
  const [sidebar, setSidebar] = useState(false);
  const [help, setHelp] = useState(false);
  const [search, setSearch] = useState("");
  useEffect(() => {
    if (
      !route.store &&
      route.page !== "missing" &&
      stores.data?.stores.length
    ) {
      const initial = stores.data.stores.includes(meta.initial_store)
        ? meta.initial_store
        : stores.data.stores[0];
      navigate(routeURL(initial, "overview"), true);
    }
  }, [route.store, route.page, stores.data, meta.initial_store, navigate]);
  useEffect(() => {
    setSidebar(false);
    setSearch("");
  }, [route.store, route.page, route.id]);
  useEffect(() => {
    const handler = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setSidebar(false);
        setHelp(false);
      }
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, []);
  const activePage = route.page === "memories" ? "library" : route.page;
  const currentNav = navigation.find((item) => item.page === activePage);
  const label = currentNav ? t(currentNav.label) : t("nav.workspace");
  const storeList = stores.data?.stores || [];
  return (
    <div className="app-shell">
      <a href="#main" className="skip-link">
        {t("shell.skipToContent")}
      </a>
      {sidebar && (
        <button
          className="sidebar-scrim"
          aria-label={t("shell.closeNavigation")}
          onClick={() => setSidebar(false)}
        />
      )}
      <aside
        className={`sidebar ${sidebar ? "open" : ""}`}
        aria-label={t("shell.workspaceNavigation")}
      >
        <Link
          href={route.store ? routeURL(route.store, "overview") : "/"}
          className="brand"
        >
          <img
            className="brand-logo"
            src="/mnemon-logo.svg"
            alt=""
            width="40"
            height="40"
          />
          <span>mnemon</span>
        </Link>
        <div className="workspace-selector">
          <label htmlFor="store-selector">{t("nav.workspace")}</label>
          <div>
            <span className="workspace-avatar">
              {(route.store || "M").slice(0, 1).toUpperCase()}
            </span>
            <select
              id="store-selector"
              aria-label={t("shell.selectStore")}
              value={route.store}
              disabled={!storeList.length}
              onChange={(event) =>
                navigate(
                  routeURL(
                    event.target.value,
                    route.page === "missing" || route.page === "memories"
                      ? "overview"
                      : route.page,
                  ),
                )
              }
            >
              {!storeList.includes(route.store) && (
                <option value={route.store}>
                  {route.store || t("shell.chooseSpace")}
                </option>
              )}
              {storeList.map((store) => (
                <option key={store} value={store}>
                  {store}
                </option>
              ))}
            </select>
          </div>
        </div>
        <div className="nav-label">{t("nav.knowledge")}</div>
        <nav>
          {navigation.map((item) => (
            <Link
              key={item.page}
              href={
                route.store
                  ? routeURL(
                      route.store,
                      item.page,
                      ["library", "graph"].includes(item.page)
                        ? filtersOnly(route.search).toString()
                        : "",
                    )
                  : "/"
              }
              className={`nav-link ${activePage === item.page ? "active" : ""}`}
              aria-current={activePage === item.page ? "page" : undefined}
            >
              <item.icon size={18} strokeWidth={1.7} />
              {t(item.label)}
              {activePage === item.page && <span className="nav-active-dot" />}
            </Link>
          ))}
        </nav>
        <div className="sidebar-bottom">
          <div className="sidebar-note">
            <div>
              <span className="live-dot" />
              {t("shell.localPrivate")}
            </div>
            <p>
              {t("shell.noteLine1")}
              <br />
              {t("shell.noteLine2")}
            </p>
            <span>
              {t("shell.readOnlyWorkspace")} <ArrowUpRight size={12} />
            </span>
          </div>
          <button className="help-button" onClick={() => setHelp(true)}>
            <CircleHelp size={17} />
            {t("shell.aboutButton")}
          </button>
          <div className="sidebar-version">
            mnemon <span>{meta.version}</span>
          </div>
        </div>
      </aside>
      <div className="main-shell">
        <header className="topbar">
          <button
            className="icon-button mobile-menu"
            aria-label={t("shell.openNavigation")}
            aria-expanded={sidebar}
            onClick={() => setSidebar(true)}
          >
            <Menu size={20} />
          </button>
          <div className="breadcrumbs">
            <span>{route.store || t("nav.workspace")}</span>
            <ChevronRight size={14} />
            <span>{label}</span>
            {route.page === "memories" && (
              <>
                <ChevronRight size={14} />
                <span>{t("nav.memory")}</span>
              </>
            )}
          </div>
          <div className="topbar-actions">
            <form
              className="global-search"
              onSubmit={(event) => {
                event.preventDefault();
                if (route.store)
                  navigate(
                    routeURL(
                      route.store,
                      "library",
                      new URLSearchParams(
                        search.trim() ? { q: search.trim() } : {},
                      ).toString(),
                    ),
                  );
              }}
            >
              <Search size={15} />
              <input
                aria-label={t("search.ariaAllMemories")}
                placeholder={t("search.placeholder")}
                value={search}
                onChange={(event) => setSearch(event.target.value)}
              />
              <button type="submit" aria-label={t("search.ariaSubmitStore")}>
                <ArrowUpRight size={14} />
              </button>
            </form>
            <button
              className="icon-button refresh-button"
              aria-label={t("topbar.refresh")}
              title={t("topbar.refresh")}
              onClick={() => {
                setRefresh((value) => value + 1);
                stores.refresh();
              }}
            >
              <RefreshCw size={16} />
            </button>
            <AppearanceControl />
            <LanguageControl />
            <span className="topbar-divider" />
            <span className="local-status">
              <span className="live-dot" />
              {t("topbar.localStatus")}
            </span>
          </div>
        </header>
        <main
          id="main"
          className={`main-content ${route.page === "memories" ? "reader-page" : ""}`}
          tabIndex={-1}
        >
          {stores.error && (
            <div className="notice warning" role="alert">
              {t("state.storeDiscoveryFailed", {
                message: stores.error.message,
              })}
              <button onClick={stores.refresh}>{t("state.retry")}</button>
            </div>
          )}
          {route.page === "missing" ? (
            <Empty title={t("missing.title")}>
              <p>
                {t("missing.return")}{" "}
                <Link href="/">{t("missing.returnLink")}</Link>.
              </p>
            </Empty>
          ) : !route.store ? (
            <QueryView query={stores}>
              {() => (
                <Empty title={t("missing.homeTitle")}>
                  <p>{t("missing.homeText")}</p>
                  <button className="button" onClick={stores.refresh}>
                    <RefreshCw size={15} />
                    {t("missing.refreshStores")}
                  </button>
                </Empty>
              )}
            </QueryView>
          ) : (
            <StoreView
              key={route.store}
              page={route.page}
              meta={meta}
              refresh={refresh}
            />
          )}
        </main>
        <footer className="app-footer">
          <span>{t("shell.footerTagline")}</span>
          <span>{t("shell.footerBrand")}</span>
        </footer>
      </div>
      {help && (
        <div className="dialog-backdrop" onClick={() => setHelp(false)}>
          <section
            className="about-dialog"
            role="dialog"
            aria-modal="true"
            aria-labelledby="about-title"
            onClick={(event) => event.stopPropagation()}
            onKeyDown={(event) => {
              if (event.key === "Tab") {
                event.preventDefault();
                event.currentTarget
                  .querySelector<HTMLElement>("button")
                  ?.focus();
              }
            }}
          >
            <button
              autoFocus
              className="icon-button dialog-close"
              aria-label={t("about.close")}
              onClick={() => setHelp(false)}
            >
              <X size={18} />
            </button>
            <img
              className="brand-logo"
              src="/mnemon-logo.svg"
              alt=""
              width="48"
              height="48"
            />
            <h2 id="about-title">{t("about.title")}</h2>
            <p>{t("about.paragraph1")}</p>
            <p>{t("about.paragraph2")}</p>
            <p>{t("about.paragraph3")}</p>
            <span className="muted">
              {t("about.version", { version: meta.version })}
            </span>
          </section>
        </div>
      )}
    </div>
  );
}
function AppearanceControl() {
  const { mode, setMode } = useTheme();
  const t = useT();
  const Icon = mode === "system" ? Monitor : mode === "dark" ? Moon : Sun;
  return (
    <label className="theme-control" title={t("appearance.label")}>
      <Icon size={16} aria-hidden="true" />
      <span className="sr-only">{t("appearance.label")}</span>
      <select
        aria-label={t("appearance.label")}
        value={mode}
        onChange={(event) => setMode(event.target.value as ThemeMode)}
      >
        <option value="system">{t("appearance.system")}</option>
        <option value="light">{t("appearance.light")}</option>
        <option value="dark">{t("appearance.dark")}</option>
      </select>
    </label>
  );
}
function LanguageControl() {
  const { locale, setLocale } = useI18n();
  const t = useT();
  return (
    <label className="theme-control" title={t("language.label")}>
      <Languages size={16} aria-hidden="true" />
      <span className="sr-only">{t("language.label")}</span>
      <select
        aria-label={t("language.label")}
        value={locale}
        onChange={(event) => setLocale(event.target.value as Locale)}
      >
        <option value="en">{t("language.en")}</option>
        <option value="zh">{t("language.zh")}</option>
      </select>
    </label>
  );
}
const pageTitles: Record<Page, keyof Dictionary> = {
  overview: "pages.overview",
  library: "pages.library",
  graph: "pages.graph",
  entities: "pages.entities",
  activity: "pages.activity",
  memories: "pages.memories",
  missing: "missing.title",
};
function StoreView({
  page,
  meta,
  refresh,
}: {
  page: Page;
  meta: Meta;
  refresh: number;
}) {
  const t = useT();
  const pageKey = pageTitles[page];
  useEffect(() => {
    document.title = t("doc.title", { page: t(pageKey) });
  }, [t, pageKey]);
  if (page === "overview")
    return <OverviewView meta={meta} refresh={refresh} />;
  if (page === "library") return <LibraryView meta={meta} refresh={refresh} />;
  if (page === "memories") return <ReaderView refresh={refresh} />;
  if (page === "graph")
    return (
      <Suspense
        fallback={
          <div className="loading-state" role="status">
            {t("state.loadingGraph")}
          </div>
        }
      >
        <GraphView meta={meta} refresh={refresh} />
      </Suspense>
    );
  if (page === "entities") return <EntitiesView refresh={refresh} />;
  if (page === "activity") return <ActivityView refresh={refresh} />;
  return null;
}
