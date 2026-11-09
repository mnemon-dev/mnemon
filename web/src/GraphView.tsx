import { useEffect, useRef } from "react";
import cytoscape, { type Core } from "cytoscape";
import {
  ArrowRight,
  Expand,
  Focus,
  Minus,
  Network,
  Plus,
  RotateCcw,
  X,
} from "lucide-react";
import { storeAPI, useQuery } from "./api";
import {
  Category,
  categoryColor,
  Empty,
  excerpt,
  humanize,
  LastUpdated,
  PageHeading,
  QueryView,
  title,
} from "./components";
import { Filters } from "./Filters";
import { useT } from "./i18n";
import { filtersOnly, Link, routeURL, useRouter } from "./router";
import type { GraphData, Insight, Meta } from "./types";
import { useTheme } from "./theme";

function graphPalette() {
  const styles = getComputedStyle(document.documentElement);
  const value = (name: string, fallback: string) =>
    styles.getPropertyValue(name).trim() || fallback;
  return {
    label: value("--graph-label", "#55555f"),
    edge: value("--graph-edge", "#b8b8c2"),
    selected: value("--graph-selection", "#202024"),
    outline: value("--graph-outline", "#ffffff"),
  };
}

export function graphStyle(
  palette: ReturnType<typeof graphPalette>,
): cytoscape.StylesheetJson {
  return [
    {
      selector: "node",
      style: {
        "background-color": "data(color)",
        label: "data(label)",
        width: "data(size)",
        height: "data(size)",
        "font-size": 10,
        "font-family": "Space Grotesk, sans-serif",
        color: palette.label,
        "text-valign": "bottom",
        "text-margin-y": 8,
        "text-max-width": "120px",
        "text-wrap": "ellipsis",
        "border-width": 4,
        "border-color": palette.outline,
        "border-opacity": 0.9,
      },
    },
    {
      selector: "node:selected",
      style: {
        "border-color": palette.selected,
        "border-width": 5,
        "text-outline-color": palette.outline,
        "text-outline-width": 3,
        color: palette.selected,
      },
    },
    {
      selector: "edge",
      style: {
        width: 1.25,
        "line-color": palette.edge,
        "target-arrow-color": palette.edge,
        "target-arrow-shape": "triangle",
        "curve-style": "bezier",
        "arrow-scale": 0.7,
        opacity: 0.8,
      },
    },
    {
      selector: 'edge[supersedes = "yes"]',
      style: {
        "line-color": "#c19857",
        "target-arrow-color": "#c19857",
        "line-style": "dashed",
        color: palette.label,
        label: "data(label)",
        "font-size": 9,
        "text-rotation": "autorotate",
        "text-background-color": palette.outline,
        "text-background-opacity": 1,
        "text-background-padding": "3px",
      },
    },
  ];
}

function GraphCanvas({
  data,
  selected,
  onSelect,
  focus,
}: {
  data: GraphData;
  selected: string;
  onSelect: (id: string) => void;
  focus: string;
}) {
  const { resolved } = useTheme();
  const t = useT();
  const tRef = useRef(t);
  tRef.current = t;
  const container = useRef<HTMLDivElement>(null);
  const graph = useRef<Core | null>(null);
  const selectCallback = useRef(onSelect);
  selectCallback.current = onSelect;
  useEffect(() => {
    if (!container.current) return;
    const palette = graphPalette();
    const nodeIDs = new Set(data.nodes.map((node) => node.id));
    const instance = cytoscape({
      container: container.current,
      elements: [
        ...data.nodes.map((node) => ({
          data: {
            id: node.id,
            label:
              title(node.content).slice(0, 36) ||
              tRef.current("state.untitled"),
            color: categoryColor(node.category),
            size: 20 + node.importance * 5,
          },
        })),
        ...(data.edges || [])
          .filter(
            (edge) =>
              nodeIDs.has(edge.source_id) && nodeIDs.has(edge.target_id),
          )
          .map((edge, index) => ({
            data: {
              id: `edge-${index}`,
              source: edge.source_id,
              target: edge.target_id,
              label: edge.edge_type,
              supersedes: edge.edge_type === "supersedes" ? "yes" : "no",
            },
          })),
      ],
      style: graphStyle(palette),
      layout: {
        name: "cose",
        animate: false,
        padding: 55,
        nodeRepulsion: () => 7000,
        idealEdgeLength: () => 95,
        randomize: false,
      },
      minZoom: 0.15,
      maxZoom: 3,
      wheelSensitivity: 0.25,
    });
    graph.current = instance;
    instance.on("tap", "node", (event) =>
      selectCallback.current(event.target.id()),
    );
    const resize = new ResizeObserver(() => instance.resize());
    resize.observe(container.current);
    return () => {
      resize.disconnect();
      instance.destroy();
      graph.current = null;
    };
  }, [data]);
  useEffect(() => {
    const instance = graph.current;
    if (!instance) return;
    const palette = graphPalette();
    // Update the canvas palette without resetting the user's layout or zoom.
    instance.style(graphStyle(palette));
  }, [resolved, data]);
  useEffect(() => {
    const instance = graph.current;
    if (!instance) return;
    instance.nodes().unselect();
    if (selected) instance.getElementById(selected).select();
  }, [selected, data]);
  return (
    <div className="graph-surface">
      <div
        className="graph-canvas"
        ref={container}
        role="img"
        aria-label={t("graph.canvasAria")}
      />
      <div className="graph-instructions">
        <span className="live-dot" />
        {focus ? t("graph.focused") : t("graph.full")}
        <span>{t("graph.dragHint")}</span>
      </div>
      <div className="graph-controls">
        <button
          aria-label={t("graph.zoomIn")}
          title={t("graph.zoomIn")}
          onClick={() => {
            const cy = graph.current;
            if (cy)
              cy.zoom({
                level: cy.zoom() * 1.3,
                renderedPosition: { x: cy.width() / 2, y: cy.height() / 2 },
              });
          }}
        >
          <Plus size={18} />
        </button>
        <button
          aria-label={t("graph.zoomOut")}
          title={t("graph.zoomOut")}
          onClick={() => {
            const cy = graph.current;
            if (cy)
              cy.zoom({
                level: cy.zoom() / 1.3,
                renderedPosition: { x: cy.width() / 2, y: cy.height() / 2 },
              });
          }}
        >
          <Minus size={18} />
        </button>
        <button
          aria-label={t("graph.fit")}
          title={t("graph.fit")}
          onClick={() => graph.current?.fit(undefined, 55)}
        >
          <Expand size={17} />
        </button>
        <button
          aria-label={t("graph.resetLayout")}
          title={t("graph.resetLayout")}
          onClick={() =>
            graph.current
              ?.layout({
                name: "cose",
                animate: false,
                padding: 55,
                randomize: true,
              })
              .run()
          }
        >
          <RotateCcw size={16} />
        </button>
      </div>
    </div>
  );
}
export function GraphView({ meta, refresh }: { meta: Meta; refresh: number }) {
  const { route, navigate } = useRouter();
  const t = useT();
  const params = filtersOnly(route.search);
  const routeParams = new URLSearchParams(route.search);
  const focus = routeParams.get("focus") || "";
  const edgeType = routeParams.get("edge_type") || "";
  params.set("limit", "200");
  if (focus) params.set("focus", focus);
  if (edgeType) params.set("edge_type", edgeType);
  const url = storeAPI(route.store, "graph", params);
  const query = useQuery<GraphData>(url, refresh);
  const selectedID = routeParams.has("selected")
    ? routeParams.get("selected")!
    : focus;
  const select = (id: string) => update("selected", id);
  const selected = query.data?.nodes.find((node) => node.id === selectedID);
  function update(key: string, value: string) {
    const next = new URLSearchParams(route.search);
    if (value || key === "selected") next.set(key, value);
    else next.delete(key);
    if (key === "focus") next.delete("selected");
    navigate(routeURL(route.store, "graph", next.toString()));
  }
  return (
    <>
      <PageHeading
        eyebrow={t("graph.eyebrow")}
        title={t("graph.title")}
        description={t("graph.description")}
      />
      <Filters meta={meta} graph />
      <div className="graph-toolbar">
        <label>
          {t("graph.relationship")}
          <select
            aria-label={t("graph.relationshipAria")}
            value={edgeType}
            onChange={(event) => update("edge_type", event.target.value)}
          >
            <option value="">{t("graph.allRelationships")}</option>
            {meta.edge_types.map((type) => (
              <option key={type} value={type}>
                {humanize(type)}
              </option>
            ))}
          </select>
        </label>
        {focus && (
          <button className="button subtle" onClick={() => update("focus", "")}>
            <X size={14} />
            {t("graph.clearFocus")}
          </button>
        )}
        <div className="graph-legend">
          {meta.categories.map((category) => (
            <Category key={category} value={category} />
          ))}
        </div>
      </div>
      <QueryView query={query}>
        {(data) => (
          <>
            {data.nodes?.length ? (
              <>
                <div
                  className={`graph-workspace ${selected ? "has-selection" : ""}`}
                >
                  <GraphCanvas
                    data={data}
                    selected={selectedID}
                    onSelect={select}
                    focus={focus}
                  />
                  {selected && (
                    <GraphPreview
                      memory={selected}
                      onClose={() => select("")}
                      onExpand={() => update("focus", selected.id)}
                    />
                  )}
                </div>
                <div className="results-line">
                  <span>
                    {t("graph.resultLine", {
                      shown: data.nodes.length,
                      total: data.total,
                      edges: data.edges?.length || 0,
                    })}
                  </span>
                  <LastUpdated
                    value={data.updated_at}
                    loading={query.loading}
                  />
                </div>
                {data.truncated && (
                  <p className="notice">{t("graph.bounded")}</p>
                )}
                <details className="graph-list" open>
                  <summary>
                    {t("graph.browseMemories")} <span>{data.nodes.length}</span>
                  </summary>
                  <div className="graph-node-list">
                    {data.nodes.map((node) => (
                      <button
                        key={node.id}
                        className={selectedID === node.id ? "active" : ""}
                        onClick={() => select(node.id)}
                        aria-pressed={selectedID === node.id}
                      >
                        <Category value={node.category} />
                        <span>
                          {title(node.content) || t("state.untitled")}
                        </span>
                        <Focus size={14} />
                      </button>
                    ))}
                  </div>
                </details>
                {!!data.edges?.length && (
                  <details className="graph-list">
                    <summary>
                      {t("graph.storedRelationships")}{" "}
                      <span>{data.edges.length}</span>
                    </summary>
                    <div className="relationship-list">
                      {data.edges.map((edge, index) => (
                        <div key={index}>
                          <Link
                            href={routeURL(
                              route.store,
                              "memories",
                              "",
                              edge.source_id,
                            )}
                          >
                            {edge.source_id}
                          </Link>
                          <span> → {edge.edge_type} → </span>
                          <Link
                            href={routeURL(
                              route.store,
                              "memories",
                              "",
                              edge.target_id,
                            )}
                          >
                            {edge.target_id}
                          </Link>
                          <small>
                            {t("graph.weight", { value: edge.weight })}
                            {Object.entries(edge.metadata || {})
                              .map(([key, value]) => ` · ${key}: ${value}`)
                              .join("")}
                          </small>
                        </div>
                      ))}
                    </div>
                  </details>
                )}
              </>
            ) : (
              <Empty title={t("graph.noConnections")} icon="search">
                <p>{t("graph.noConnectionsText")}</p>
              </Empty>
            )}
          </>
        )}
      </QueryView>
    </>
  );
}
function GraphPreview({
  memory,
  onClose,
  onExpand,
}: {
  memory: Insight;
  onClose: () => void;
  onExpand: () => void;
}) {
  const { route } = useRouter();
  const t = useT();
  return (
    <aside className="graph-preview">
      <div className="section-heading">
        <span className="eyebrow">{t("graph.selectedMemory")}</span>
        <button
          className="icon-button"
          aria-label={t("graph.closeSelected")}
          onClick={onClose}
        >
          <X size={17} />
        </button>
      </div>
      <Category value={memory.category} />
      <h2>{title(memory.content) || t("state.untitled")}</h2>
      <p>{excerpt(memory.content)}</p>
      <dl>
        <dt>{t("reader.source")}</dt>
        <dd>{memory.source || t("state.notRecorded")}</dd>
      </dl>
      <Link
        className="button primary"
        href={routeURL(route.store, "memories", route.search, memory.id)}
      >
        {t("graph.readFullMemory")}
        <ArrowRight size={15} />
      </Link>
      <button className="button" onClick={onExpand}>
        <Network size={15} />
        {t("graph.expandNeighborhood")}
      </button>
      {!!memory.entities?.length && (
        <div className="chip-list">
          {memory.entities.map((entity) => (
            <Link
              href={routeURL(
                route.store,
                "library",
                new URLSearchParams({ entity }).toString(),
              )}
              key={entity}
            >
              {entity}
            </Link>
          ))}
        </div>
      )}
    </aside>
  );
}
