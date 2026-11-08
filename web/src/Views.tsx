import { useEffect, useState } from "react";
import {
  ArrowLeft,
  ArrowRight,
  BookOpen,
  Clock3,
  Hash,
  Network,
  Search,
  Shapes,
} from "lucide-react";
import { storeAPI, useQuery } from "./api";
import {
  Category,
  CopyButton,
  date,
  Empty,
  Importance,
  LastUpdated,
  Markdown,
  MemoryCard,
  PageHeading,
  QueryView,
  ReaderLink,
  SectionTitle,
  title,
} from "./components";
import { Filters } from "./Filters";
import { useT } from "./i18n";
import { filtersOnly, Link, routeURL, useRouter } from "./router";
import type {
  ActivityPage,
  EntityPage,
  MemoryDetail,
  MemoryPage,
  Meta,
  Overview,
} from "./types";

export function OverviewView({
  meta,
  refresh,
}: {
  meta: Meta;
  refresh: number;
}) {
  const { route } = useRouter();
  const t = useT();
  const query = useQuery<Overview>(storeAPI(route.store, "overview"), refresh);
  return (
    <>
      <PageHeading
        eyebrow={t("overview.eyebrow")}
        title={t("overview.title")}
        description={t("overview.description")}
        action={
          <Link
            className="button primary"
            href={routeURL(route.store, "library")}
          >
            <BookOpen size={16} />
            {t("overview.explore")}
            <ArrowRight size={15} />
          </Link>
        }
      />
      <QueryView query={query}>
        {(data) => (
          <>
            <div className="overview-banner">
              <div className="banner-mark">
                <img src="/mnemon-logo.svg" alt="" width="56" height="56" />
              </div>
              <div>
                <span className="eyebrow">Mnemon</span>
                <h2>{t("overview.bannerTitle")}</h2>
                <p>{t("overview.bannerText")}</p>
              </div>
              <div className="banner-orbit" aria-hidden="true">
                <span />
                <span />
                <span />
                <i />
                <i />
                <i />
              </div>
            </div>
            <div className="stats-row">
              <div className="stat-card">
                <span>
                  <BookOpen size={16} />
                  {t("overview.statMemories")}
                </span>
                <strong>{data.total.toLocaleString()}</strong>
                <small>{t("overview.statMemoriesHint")}</small>
              </div>
              <div className="stat-card">
                <span>
                  <Network size={16} />
                  {t("overview.statConnections")}
                </span>
                <strong>{data.edge_count.toLocaleString()}</strong>
                <small>{t("overview.statConnectionsHint")}</small>
              </div>
              <div className="stat-card">
                <span>
                  <Shapes size={16} />
                  {t("overview.statCategories")}
                </span>
                <strong>
                  {
                    Object.values(data.by_category || {}).filter(
                      (count) => count > 0,
                    ).length
                  }
                </strong>
                <small>{t("overview.statCategoriesHint")}</small>
              </div>
            </div>
            <SectionTitle>{t("overview.byCategory")}</SectionTitle>
            <div className="category-grid">
              {[
                ...new Set([
                  ...meta.categories,
                  ...Object.keys(data.by_category || {}),
                ]),
              ].map((category) => (
                <Link
                  className="category-tile"
                  key={category}
                  href={routeURL(
                    route.store,
                    "library",
                    new URLSearchParams({ category }).toString(),
                  )}
                >
                  <Category value={category} />
                  <strong>
                    {(data.by_category?.[category] || 0).toLocaleString()}
                  </strong>
                  <ArrowRight size={15} />
                </Link>
              ))}
            </div>
            <div className="overview-columns">
              <section>
                <SectionTitle href={routeURL(route.store, "library")}>
                  {t("overview.recentSection")}
                </SectionTitle>
                {data.recent?.length ? (
                  <div className="recent-list">
                    {data.recent.slice(0, 6).map((memory) => (
                      <MemoryCard key={memory.id} memory={memory} compact />
                    ))}
                  </div>
                ) : (
                  <Empty title={t("overview.recentEmptyTitle")}>
                    <p>{t("overview.recentEmptyText")}</p>
                  </Empty>
                )}
              </section>
              <aside>
                <SectionTitle href={routeURL(route.store, "entities")}>
                  {t("overview.entitiesSection")}
                </SectionTitle>
                <div className="entity-panel">
                  {data.entities?.length ? (
                    data.entities.slice(0, 8).map((entity) => (
                      <Link
                        key={entity.name}
                        href={routeURL(
                          route.store,
                          "library",
                          new URLSearchParams({
                            entity: entity.name,
                          }).toString(),
                        )}
                      >
                        <span className="entity-avatar">
                          <Hash size={15} />
                        </span>
                        <span>{entity.name}</span>
                        <span className="entity-count">{entity.count}</span>
                        <ArrowRight size={14} />
                      </Link>
                    ))
                  ) : (
                    <p className="muted">{t("overview.noEntities")}</p>
                  )}
                  <div className="panel-note">
                    <Network size={19} />
                    <p>
                      {t("overview.followConnections")}
                      <br />
                      <Link href={routeURL(route.store, "graph")}>
                        {t("overview.seeGraph")} <ArrowRight size={12} />
                      </Link>
                    </p>
                  </div>
                </div>
              </aside>
            </div>
            <OverviewActivity refresh={refresh} />
            <footer className="view-footer">
              <LastUpdated value={data.updated_at} loading={query.loading} />
              <span>{t("overview.footerNote")}</span>
            </footer>
          </>
        )}
      </QueryView>
    </>
  );
}
function OverviewActivity({ refresh }: { refresh: number }) {
  const { route } = useRouter();
  const t = useT();
  const query = useQuery<ActivityPage>(
    storeAPI(route.store, "activity", new URLSearchParams({ limit: "5" })),
    refresh,
  );
  return (
    <section className="overview-activity">
      <SectionTitle href={routeURL(route.store, "activity")}>
        {t("overview.activitySection")}
      </SectionTitle>
      <QueryView query={query}>
        {(data) =>
          data.items?.length ? (
            <div className="activity-list">
              {data.items.map((item) => (
                <article key={item.id} className="activity-item">
                  <div className="activity-icon">
                    <Clock3 size={15} />
                  </div>
                  <div>
                    <strong>{item.operation}</strong>
                    {item.available ? (
                      <ReaderLink id={item.insight_id} />
                    ) : (
                      <span className="unavailable">
                        {t("state.memoryUnavailable")} · {item.insight_id}
                      </span>
                    )}
                  </div>
                  <time dateTime={item.created_at}>
                    {date(item.created_at, true) || t("state.unknownDate")}
                  </time>
                </article>
              ))}
            </div>
          ) : (
            <p className="muted">{t("overview.noActivity")}</p>
          )
        }
      </QueryView>
    </section>
  );
}
export function LibraryView({
  meta,
  refresh,
}: {
  meta: Meta;
  refresh: number;
}) {
  const { route } = useRouter();
  const t = useT();
  const params = filtersOnly(route.search);
  params.set("limit", "24");
  const query = useQuery<MemoryPage>(
    storeAPI(route.store, "memories", params),
    refresh,
    true,
  );
  const facets = useQuery<Overview>(storeAPI(route.store, "overview"), refresh);
  return (
    <>
      <PageHeading
        eyebrow={t("library.eyebrow")}
        title={t("library.title")}
        description={t("library.description")}
      />
      <Filters meta={meta} facets={facets.data} />
      <QueryView query={query}>
        {(data) => (
          <>
            <div className="results-line">
              <span>
                <strong>{data.total.toLocaleString()}</strong>{" "}
                {t(data.total === 1 ? "library.unitOne" : "library.unitMany")}
                {filtersOnly(route.search).size > 0
                  ? ` ${t("library.matchingView")}`
                  : ` ${t("library.inThisSpace")}`}
              </span>
              <LastUpdated value={data.updated_at} loading={query.loading} />
            </div>
            {data.truncated && <p className="notice">{t("library.bounded")}</p>}
            {data.items?.length ? (
              <div className="memory-grid">
                {data.items.map((memory) => (
                  <MemoryCard key={memory.id} memory={memory} />
                ))}
              </div>
            ) : (
              <Empty
                title={
                  filtersOnly(route.search).size
                    ? t("library.noMatchTitle")
                    : t("library.readyTitle")
                }
                icon={filtersOnly(route.search).size ? "search" : "empty"}
              >
                <p>
                  {filtersOnly(route.search).size
                    ? t("library.noMatchText")
                    : t("library.readyText")}
                </p>
              </Empty>
            )}
            {data.next_cursor && (
              <div className="pagination">
                <button
                  className="button"
                  onClick={query.loadMore}
                  disabled={query.loading || query.loadingMore}
                >
                  {query.loadingMore
                    ? t("state.loadingMore")
                    : t("library.loadMore")}
                  <ArrowRight size={15} />
                </button>
                <span>
                  {t("library.showing", {
                    shown: data.items.length,
                    total: data.total,
                  })}
                </span>
              </div>
            )}
          </>
        )}
      </QueryView>
    </>
  );
}
export function ReaderView({ refresh }: { refresh: number }) {
  const { route } = useRouter();
  const t = useT();
  const query = useQuery<MemoryDetail>(
    storeAPI(route.store, `memories/${encodeURIComponent(route.id)}`),
    refresh,
  );
  return (
    <>
      <Link
        className="back-link"
        href={routeURL(
          route.store,
          "library",
          filtersOnly(route.search).toString(),
        )}
      >
        <ArrowLeft size={15} />
        {t("reader.backToLibrary")}
      </Link>
      <QueryView query={query} unavailable>
        {(data) => (
          <div className="reader-layout">
            <article className="reader">
              <div className="reader-kicker">
                <Category value={data.memory.category} />
                <span>{t("reader.kicker")}</span>
              </div>
              <h1>{title(data.memory.content) || t("state.untitled")}</h1>
              <div className="reader-meta">
                <span>
                  <Clock3 size={14} />
                  {date(data.memory.created_at) || t("state.unknownDate")}
                </span>
                <span>{data.memory.source || t("state.noSource")}</span>
                <Importance value={data.memory.importance} />
              </div>
              {!!data.superseded_by?.length && (
                <div className="notice warning">
                  <div>
                    <strong>{t("reader.replacedTitle")}</strong>
                    <p>{t("reader.replacedText")}</p>
                    {data.superseded_by.map((id) => (
                      <ReaderLink key={id} id={id} />
                    ))}
                  </div>
                </div>
              )}
              <Markdown content={data.memory.content} />
              <div className="reader-actions">
                <CopyButton
                  value={data.memory.content}
                  label={t("reader.copyContent")}
                />
                <CopyButton value={data.memory.id} label={t("reader.copyId")} />
              </div>
              <SectionTitle>{t("reader.relatedSection")}</SectionTitle>
              {data.related?.length ? (
                <div className="related-grid">
                  {data.related.map((memory) => (
                    <MemoryCard key={memory.id} memory={memory} compact />
                  ))}
                </div>
              ) : (
                <p className="muted">{t("reader.noRelated")}</p>
              )}
              {data.truncated && (
                <p className="notice">{t("reader.relatedBounded")}</p>
              )}
            </article>
            <aside className="reader-sidebar">
              <h2>{t("reader.detailsHeading")}</h2>
              <dl>
                <dt>{t("reader.category")}</dt>
                <dd>
                  <Category value={data.memory.category} />
                </dd>
                <dt>{t("reader.source")}</dt>
                <dd>{data.memory.source || t("state.notRecorded")}</dd>
                <dt>{t("reader.importance")}</dt>
                <dd>
                  <Importance value={data.memory.importance} />{" "}
                  {t("reader.importanceOf", { value: data.memory.importance })}
                </dd>
                <dt>{t("reader.created")}</dt>
                <dd>{date(data.memory.created_at, true)}</dd>
                <dt>{t("reader.updated")}</dt>
                <dd>{date(data.memory.updated_at, true)}</dd>
                <dt>{t("reader.accessCount")}</dt>
                <dd>{data.memory.access_count}</dd>
                <dt>{t("reader.memoryId")}</dt>
                <dd className="mono">{data.memory.id}</dd>
              </dl>
              <h2>{t("reader.entities")}</h2>
              <div className="chip-list">
                {data.memory.entities?.length ? (
                  data.memory.entities.map((entity) => (
                    <Link
                      key={entity}
                      href={routeURL(
                        route.store,
                        "library",
                        new URLSearchParams({ entity }).toString(),
                      )}
                    >
                      <Hash size={12} />
                      {entity}
                    </Link>
                  ))
                ) : (
                  <span className="muted">{t("state.noneRecorded")}</span>
                )}
              </div>
              <h2>{t("reader.tags")}</h2>
              <div className="chip-list">
                {data.memory.tags?.length ? (
                  data.memory.tags.map((tag) => (
                    <Link
                      key={tag}
                      href={routeURL(
                        route.store,
                        "library",
                        new URLSearchParams({ tag }).toString(),
                      )}
                    >
                      {tag}
                    </Link>
                  ))
                ) : (
                  <span className="muted">{t("state.noneRecorded")}</span>
                )}
              </div>
              {!!data.replaces?.length && (
                <>
                  <h2>{t("reader.replaces")}</h2>
                  <p className="muted small">{t("reader.replacesHint")}</p>
                  <div className="lineage">
                    {data.replaces.map((id) => (
                      <ReaderLink key={id} id={id} />
                    ))}
                  </div>
                </>
              )}
              {!!data.superseded_by?.length && (
                <>
                  <h2>{t("reader.replacedBy")}</h2>
                  <div className="lineage">
                    {data.superseded_by.map((id) => (
                      <ReaderLink key={id} id={id} />
                    ))}
                  </div>
                </>
              )}
              <Link
                className="button"
                href={routeURL(
                  route.store,
                  "graph",
                  new URLSearchParams({ focus: data.memory.id }).toString(),
                )}
              >
                <Network size={15} />
                {t("reader.exploreNeighborhood")}
              </Link>
              {!!data.edges?.length && (
                <>
                  <h2>{t("reader.storedRelationships")}</h2>
                  <div className="edge-details">
                    {data.edges.map((edge, index) => (
                      <div key={index}>
                        <strong>{edge.edge_type}</strong>
                        <span className="mono">
                          {edge.source_id} → {edge.target_id}
                        </span>
                        <span>
                          {t("reader.weight", { value: edge.weight })}
                        </span>
                        {Object.entries(edge.metadata || {}).map(
                          ([key, value]) => (
                            <span key={key}>
                              {key}: {value}
                            </span>
                          ),
                        )}
                      </div>
                    ))}
                  </div>
                </>
              )}
            </aside>
          </div>
        )}
      </QueryView>
    </>
  );
}
export function EntitiesView({ refresh }: { refresh: number }) {
  const { route, navigate } = useRouter();
  const t = useT();
  const params = new URLSearchParams({ limit: "50" });
  const term = new URLSearchParams(route.search).get("q") || "";
  if (term) params.set("q", term);
  const [search, setSearch] = useState(term);
  useEffect(() => setSearch(term), [term]);
  const query = useQuery<EntityPage>(
    storeAPI(route.store, "entities", params),
    refresh,
    true,
  );
  return (
    <>
      <PageHeading
        eyebrow={t("entities.eyebrow")}
        title={t("entities.title")}
        description={t("entities.description")}
      />
      <form
        className="search-field entity-search"
        onSubmit={(event) => {
          event.preventDefault();
          navigate(
            routeURL(
              route.store,
              "entities",
              new URLSearchParams(
                search.trim() ? { q: search.trim() } : {},
              ).toString(),
            ),
          );
        }}
      >
        <Search size={18} />
        <input
          aria-label={t("entities.searchAria")}
          placeholder={t("entities.searchPlaceholder")}
          value={search}
          onChange={(event) => setSearch(event.target.value)}
        />
        <button type="submit" className="search-submit">
          {t("search.submit")}
        </button>
      </form>
      <QueryView query={query}>
        {(data) => (
          <>
            <div className="results-line">
              <span>
                <strong>{data.total}</strong> {t("entities.unit")}
              </span>
              <LastUpdated value={data.updated_at} loading={query.loading} />
            </div>
            {data.items?.length ? (
              <div className="entities-grid">
                {data.items.map((entity) => (
                  <article key={entity.name} className="entity-card">
                    <div className="entity-avatar">
                      <Hash size={20} />
                    </div>
                    <h2>{entity.name}</h2>
                    <p>
                      {t(
                        entity.count === 1
                          ? "entities.memoryCountOne"
                          : "entities.memoryCountMany",
                        { count: entity.count },
                      )}
                    </p>
                    <div>
                      <Link
                        href={routeURL(
                          route.store,
                          "library",
                          new URLSearchParams({
                            entity: entity.name,
                          }).toString(),
                        )}
                      >
                        {t("entities.viewMemories")}
                        <ArrowRight size={13} />
                      </Link>
                      <Link
                        aria-label={t("entities.exploreInGraph", {
                          name: entity.name,
                        })}
                        href={routeURL(
                          route.store,
                          "graph",
                          new URLSearchParams({
                            entity: entity.name,
                          }).toString(),
                        )}
                      >
                        <Network size={17} />
                      </Link>
                    </div>
                  </article>
                ))}
              </div>
            ) : (
              <Empty
                title={
                  term ? t("entities.noMatchTitle") : t("entities.emptyTitle")
                }
                icon={term ? "search" : "empty"}
              >
                <p>
                  {term ? t("entities.noMatchText") : t("entities.emptyText")}
                </p>
              </Empty>
            )}
            {data.next_cursor && (
              <div className="pagination">
                <button
                  className="button"
                  disabled={query.loading || query.loadingMore}
                  onClick={query.loadMore}
                >
                  {query.loadingMore
                    ? t("state.loadingMore")
                    : t("entities.loadMore")}
                </button>
              </div>
            )}
          </>
        )}
      </QueryView>
    </>
  );
}
export function ActivityView({ refresh }: { refresh: number }) {
  const { route } = useRouter();
  const t = useT();
  const query = useQuery<ActivityPage>(
    storeAPI(route.store, "activity", new URLSearchParams({ limit: "50" })),
    refresh,
    true,
  );
  return (
    <>
      <PageHeading
        eyebrow={t("activity.eyebrow")}
        title={t("activity.title")}
        description={t("activity.description")}
      />
      <QueryView query={query}>
        {(data) => (
          <>
            <div className="results-line">
              <span>{t("activity.operationLog")}</span>
              <LastUpdated value={data.updated_at} loading={query.loading} />
            </div>
            {data.items?.length ? (
              <div className="activity-list">
                {data.items.map((item) => (
                  <article key={item.id} className="activity-item">
                    <div className="activity-icon">
                      <Clock3 size={16} />
                    </div>
                    <div>
                      <strong>{item.operation}</strong>
                      {item.available ? (
                        <ReaderLink id={item.insight_id} />
                      ) : (
                        <span className="unavailable">
                          {t("state.memoryUnavailable")}{" "}
                          <code>{item.insight_id}</code>
                        </span>
                      )}
                    </div>
                    <time dateTime={item.created_at}>
                      {date(item.created_at, true) || t("state.unknownDate")}
                    </time>
                  </article>
                ))}
              </div>
            ) : (
              <Empty title={t("activity.quietTitle")}>
                <p>{t("activity.quietText")}</p>
              </Empty>
            )}
            {data.next_cursor && (
              <div className="pagination">
                <button
                  className="button"
                  disabled={query.loading || query.loadingMore}
                  onClick={query.loadMore}
                >
                  {query.loadingMore
                    ? t("state.loadingMore")
                    : t("activity.loadMore")}
                </button>
              </div>
            )}
          </>
        )}
      </QueryView>
    </>
  );
}
