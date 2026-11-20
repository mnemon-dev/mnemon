import { useEffect, useRef, useState, type RefObject } from "react";
import cytoscape, { type Core, type NodeSingular } from "cytoscape";
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
  attachDragWheelZoom,
  createGraphPhysics,
  GRAPH_WHEEL_SENSITIVITY,
  type GraphPhysics,
  type PhysicsElements,
} from "./graphPhysics";
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

interface GraphRelationship {
  id: string;
  source: string;
  target: string;
  type: string;
  weight: number;
}

function graphPalette() {
  const styles = getComputedStyle(document.documentElement);
  const value = (name: string, fallback: string) =>
    styles.getPropertyValue(name).trim() || fallback;
  return {
    label: value("--graph-label", "#55555f"),
    edge: value("--graph-edge", "#b8b8c2"),
    selected: value("--graph-selection", "#202024"),
    outline: value("--graph-outline", "#ffffff"),
    highlight: value("--graph-highlight", "#9fc2f0"),
  };
}

export function graphStyle(
  palette: ReturnType<typeof graphPalette>,
): cytoscape.StylesheetJson {
  return [
    {
      // Keep a small, theme-aware press cue on the background instead of the
      // default 60px black disk, which resembles an extra graph node.
      selector: "core",
      style: {
        "active-bg-color": palette.highlight,
        "active-bg-size": 6,
        "active-bg-opacity": 0.12,
      } as cytoscape.Css.Core, // Core rules can be partial; the typings require all properties.
    },
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
        // These colors must stay stable while their widths/opacities fade out;
        // otherwise deselection briefly restores Cytoscape's black defaults.
        "text-outline-color": palette.outline,
        "text-outline-width": 0,
        "underlay-color": palette.highlight,
        "underlay-opacity": 0,
        "underlay-padding": 5,
        // Semantic zoom: labels stay hidden until the zoom tier (or the
        // explicit mode/hover) grants the "labeled"/"hovered" class.
        "text-opacity": 0,
        "border-width": 4,
        "border-color": palette.outline,
        "border-opacity": 0.9,
        // ~200ms easing covers labels, fade levels, the soft halo and the
        // selection stroke so every emphasis change eases instead of popping.
        "transition-property":
          "text-opacity, opacity, underlay-opacity, underlay-padding, border-color, border-width, text-outline-width, color",
        "transition-duration": 200,
      },
    },
    {
      selector: "node.labeled, node.hovered, node:selected, node.focus-set",
      style: {
        "text-opacity": 1,
      },
    },
    {
      // Selection emphasis stays subtractive: a theme-adapted accent stroke
      // (never near-black/near-white) and a thin label knock-out, eased in
      // over 200ms like every other emphasis change.
      selector: "node:selected",
      style: {
        "border-color": palette.highlight,
        "border-width": 5,
        "text-outline-color": palette.outline,
        "text-outline-width": 2,
        color: palette.label,
      },
    },
    {
      // Subtractive emphasis: unrelated nodes stay readable on light and
      // dark backgrounds alike (top of the ~0.25-0.35 band).
      selector: "node.faded",
      style: {
        opacity: 0.35,
      },
    },
    {
      // The soft halo marks the single anchor node (selected / focused)
      // only — never the whole neighborhood.
      selector: "node.highlight",
      style: {
        "underlay-color": palette.highlight,
        "underlay-opacity": 0.45,
        "underlay-padding": 5,
      },
    },
    {
      // Element-local hover: a gentle accent stroke plus the label. No
      // whole-graph fading on hover (§6).
      selector: "node.hovered",
      style: {
        "border-color": palette.highlight,
      },
    },
    {
      // At most the hovered node's direct edges lift slightly.
      selector: "edge.hover-link",
      style: {
        opacity: 0.8,
      },
    },
    {
      // Weight-mapped rendering: width and opacity come from element data so
      // weak edges stay thin and faint while strong ones stand out.
      selector: "edge",
      style: {
        width: "data(width)",
        "line-color": palette.edge,
        "target-arrow-color": palette.edge,
        "target-arrow-shape": "triangle",
        "curve-style": "bezier",
        "arrow-scale": 0.7,
        // Data mappers are strings at runtime; the typings model plain values.
        opacity: "data(opacity)" as unknown as number,
        "transition-property": "opacity",
        "transition-duration": 200,
      },
    },
    {
      // Preserve edge presses without the default 10px black overlay that
      // turns thin connections into broad dark bands.
      selector: "edge:active",
      style: {
        "overlay-color": palette.highlight,
        "overlay-padding": 1,
        "overlay-opacity": 0.12,
      },
    },
    {
      // Focus dimming keeps faded edges clearly visible on the light theme's
      // near-white canvas; selected links get a separate, modest contrast lift.
      selector: "edge.faded",
      style: {
        opacity: 0.3,
      },
    },
    {
      // Zoom-density dimming (low zoom decluttering) stays a notch lower.
      selector: "edge.zoom-dim",
      style: {
        opacity: 0.25,
      },
    },
    {
      // Keep the selected node's direct connections easy to trace, including
      // weak edges dimmed by semantic zoom. Reuse the 200ms opacity easing.
      selector: "edge.selected-link",
      style: {
        opacity: 0.85,
      },
    },
    {
      selector: "edge.inspected",
      style: {
        "line-color": palette.highlight,
        "target-arrow-color": palette.highlight,
        opacity: 0.9,
      },
    },
    {
      // Display-only edge filtering: elements stay in the graph and keep the
      // physics layout; only their rendering is suppressed.
      selector: "edge.mode-hidden",
      style: {
        display: "none",
      },
    },
    {
      selector: 'edge[supersedes = "yes"]',
      style: {
        "line-color": "#c19857",
        "target-arrow-color": "#c19857",
        "line-style": "dashed",
        "arrow-scale": 1.2,
        color: palette.label,
        label: "data(label)",
        "font-size": 10,
        "text-opacity": 0,
        "text-rotation": "autorotate",
        "text-background-color": palette.outline,
        "text-background-opacity": 1,
        "text-background-padding": "3px",
      },
    },
    {
      selector: 'edge[supersedes = "yes"].zoom-overview',
      style: { "arrow-scale": 1.8 },
    },
    {
      selector: 'edge[supersedes = "yes"].labeled',
      style: { "text-opacity": 1 },
    },
  ];
}

// Edges at or above this weight count as "strong" for display filtering and
// zoom-density dimming. Display layer only — never affects stored relations.
const STRONG_EDGE_WEIGHT = 0.6;

// Weight-tiered edge visuals: thin/faint by default, stronger edges stand out.
function edgeVisuals(weight: number) {
  const clamped =
    Number.isFinite(weight) && weight > 0 ? Math.min(weight, 1) : 0.2;
  const width =
    clamped < 0.3
      ? 0.8
      : clamped < 0.5
        ? 1.1
        : clamped < 0.7
          ? 1.5
          : clamped < 0.9
            ? 2.2
            : 3;
  return {
    width,
    opacity: 0.35 + 0.5 * clamped,
    strong: clamped >= STRONG_EDGE_WEIGHT ? "yes" : "no",
  };
}

// Element definitions keyed by stable ids so polling refreshes can be applied
// as a diff instead of rebuilding the instance. Edge ids derive from their
// endpoints and type (with a counter for duplicates) so they survive reorders.
function graphElements(data: GraphData, untitled: string): PhysicsElements {
  const nodeIDs = new Set(data.nodes.map((node) => node.id));
  const edgeIDCounts = new Map<string, number>();
  return {
    nodes: data.nodes.map((node) => ({
      data: {
        id: node.id,
        label: title(node.content).slice(0, 36) || untitled,
        fullTitle: title(node.content) || untitled,
        color: categoryColor(node.category),
        size: 20 + node.importance * 5,
        importance: node.importance,
      },
    })),
    edges: (data.edges || [])
      .filter(
        (edge) => nodeIDs.has(edge.source_id) && nodeIDs.has(edge.target_id),
      )
      .map((edge) => {
        const base = `${edge.source_id}:${edge.target_id}:${edge.edge_type}`;
        const count = (edgeIDCounts.get(base) || 0) + 1;
        edgeIDCounts.set(base, count);
        const visuals = edgeVisuals(edge.weight);
        return {
          // Edges open relationship details; native selection must not clear
          // the node selection that is owned by the URL.
          selectable: false,
          data: {
            id: count === 1 ? base : `${base}#${count}`,
            source: edge.source_id,
            target: edge.target_id,
            label: edge.edge_type,
            weight: edge.weight,
            width: visuals.width,
            opacity: visuals.opacity,
            strong: visuals.strong,
            supersedes: edge.edge_type === "supersedes" ? "yes" : "no",
          },
        };
      }),
  };
}

function GraphCanvas({
  data,
  selected,
  onSelect,
  focus,
  localFocus,
  localHops,
  viewMode,
  canFocus,
  onViewMode,
  locateRef,
  relationship,
  onInspectEdge,
}: {
  data: GraphData;
  selected: string;
  onSelect: (id: string) => void;
  focus: string;
  localFocus: string;
  localHops: 1 | 2;
  viewMode: "global" | "focus" | "focus2";
  canFocus: boolean;
  onViewMode: (mode: "global" | "focus" | "focus2") => void;
  locateRef: RefObject<((id: string) => void) | null>;
  relationship: GraphRelationship | null;
  onInspectEdge: (edge: GraphRelationship | null) => void;
}) {
  const { resolved } = useTheme();
  const t = useT();
  const tRef = useRef(t);
  tRef.current = t;
  const container = useRef<HTMLDivElement>(null);
  const graph = useRef<Core | null>(null);
  const physics = useRef<GraphPhysics | null>(null);
  const selectCallback = useRef(onSelect);
  selectCallback.current = onSelect;
  const [labelMode, setLabelMode] = useState<"auto" | "all" | "hidden">("auto");
  const [edgeMode, setEdgeMode] = useState<"all" | "strong" | "selected">(
    "all",
  );
  const closeEdgeDetails = () => onInspectEdge(null);
  const inspectedEdge = relationship?.id || "";
  const labelModeRef = useRef(labelMode);
  const applyVisuals = useRef(() => {});
  const applyEmphasis = useRef(() => {});
  // Emphasis inputs shared by hover, selection and local-focus highlighting.
  const emphasisState = useRef({
    selected: "",
    focus: "",
    hops: 2 as 1 | 2,
  });
  const prevLocalFocus = useRef("");
  // The Cytoscape instance is created once per mount; data refreshes are
  // applied as element diffs by the sync effect below so positions, zoom and
  // pan survive polling instead of triggering a full rebuild.
  useEffect(() => {
    if (!container.current) return;
    const palette = graphPalette();
    const instance = cytoscape({
      container: container.current,
      elements: [],
      // Positions come exclusively from the force simulation; keep the
      // constructor from running a one-shot layout over the empty set.
      layout: { name: "preset" },
      style: graphStyle(palette),
      minZoom: 0.15,
      maxZoom: 3,
      wheelSensitivity: GRAPH_WHEEL_SENSITIVITY,
    });
    graph.current = instance;
    const sim = createGraphPhysics(instance);
    physics.current = sim;
    locateRef.current = (id) => {
      const node = instance.getElementById(id);
      if (!node.nonempty()) return;
      closeEdgeDetails();
      // List navigation owns the viewport, including while the initial layout
      // is settling. Preserve closer user zooms and never queue camera moves.
      sim.cancelSettleFit();
      instance.resize();
      instance.stop(true, false);
      const zoom = Math.max(instance.zoom(), 1.4);
      const position = node.position();
      instance.animate(
        {
          zoom,
          pan: {
            x: instance.width() / 2 - position.x * zoom,
            y: instance.height() / 2 - position.y * zoom,
          },
        },
        { duration: 240, easing: "ease-in-out-cubic" },
      );
      container.current?.scrollIntoView({ behavior: "smooth", block: "center" });
    };
    instance.on("tap", "node", (event) => {
      closeEdgeDetails();
      selectCallback.current(event.target.id());
    });
    instance.on("tap", "edge", (event) => {
      const edge = event.target;
      onInspectEdge({
        id: edge.id(),
        source: String(edge.source().data("fullTitle")),
        target: String(edge.target().data("fullTitle")),
        type: String(edge.data("label")),
        weight: Number(edge.data("weight")),
      });
    });
    instance.on("tap", (event) => {
      if (event.target === instance) closeEdgeDetails();
    });
    // Drag gestures drive the simulation: the grabbed node is pinned, the
    // forces stay hot while dragging, and the network rebounds on release.
    let draggingNode = false;
    instance.on("grab", "node", (event) => sim.onGrab(event.target.id()));
    instance.on("drag", "node", (event) => {
      draggingNode = true;
      sim.onDrag(event.target.id());
    });
    instance.on("free", "node", (event) => {
      draggingNode = false;
      sim.onFree(event.target.id());
    });
    // Cytoscape blocks wheel zooming during drag gestures; the companion
    // listener keeps zooming alive there, anchored at the cursor.
    const detachWheel = attachDragWheelZoom(instance, container.current, {
      wheelSensitivity: GRAPH_WHEEL_SENSITIVITY,
      isDragging: () => draggingNode,
    });
    // Semantic zoom: label tiers follow the zoom level, important nodes first.
    // Throttled trailing-edge so continuous wheel zooming stays cheap and no
    // React state is involved — only Cytoscape classes.
    const weakEdges = () =>
      instance
        .edges()
        .filter(
          (edge) =>
            edge.data("supersedes") !== "yes" &&
            Number(edge.data("weight")) < STRONG_EDGE_WEIGHT,
        );
    const labelTiers = () => {
      const mode = labelModeRef.current;
      const zoom = instance.zoom();
      // Local focus mode keeps every neighborhood label visible (§3).
      const focused = !!emphasisState.current.focus;
      // Low zoom shows only the most important labels; zooming in reveals
      // more, and past ~1.25 everything is labelled.
      // Thresholds calibrated on real stores where most memories sit at
      // importance 3+ (e.g. 200-node window: 9x2, 112x3, 72x4, 7x5), so the
      // tiers must climb higher to keep low zoom readable.
      const minImportance =
        mode !== "auto"
          ? Number.POSITIVE_INFINITY
          : focused
            ? 0
            : zoom < 0.45
              ? 5
              : zoom < 0.9
                ? 4
                : zoom < 1.4
                  ? 3
                  : 0;
      const dimWeak = mode === "auto" && zoom < 0.8 && !focused;
      instance.batch(() => {
        for (const node of instance.nodes()) {
          const show =
            mode === "all" ||
            (mode === "auto" &&
              (Number(node.data("importance")) || 0) >= minImportance);
          node.toggleClass("labeled", show);
        }
        weakEdges().toggleClass("zoom-dim", dimWeak);
        const corrections = instance.edges('[supersedes = "yes"]');
        corrections.toggleClass("zoom-overview", zoom < 0.9);
        corrections.toggleClass(
          "labeled",
          mode === "all" || (mode === "auto" && (focused || zoom >= 0.9)),
        );
      });
    };
    applyVisuals.current = labelTiers;
    let zoomTimer: ReturnType<typeof setTimeout> | undefined;
    instance.on("zoom", () => {
      clearTimeout(zoomTimer);
      zoomTimer = setTimeout(labelTiers, 100);
    });
    // Click-level emphasis (selection / two-hop focus): the union of the
    // relevant neighborhoods keeps full rendering while only the unrelated
    // remainder fades. Runs on selection or mode changes — never per hover
    // event — so the O(N) fade pass cannot fire while sweeping the cursor.
    applyEmphasis.current = () => {
      const state = emphasisState.current;
      const keepParts = [];
      const node = (id: string) => {
        if (!id) return null;
        const found = instance.getElementById(id);
        return found.nonempty() && found.isNode() ? found : null;
      };
      const selectedNode = node(state.selected);
      if (selectedNode) keepParts.push(selectedNode.closedNeighborhood());
      const focusNode =
        state.focus && state.hops === 2 ? node(state.focus) : null;
      if (focusNode) {
        // Two-hop expansion over the current dataset (display layer only).
        keepParts.push(focusNode.closedNeighborhood().closedNeighborhood());
      }
      const all = instance.elements();
      const keep =
        keepParts.length > 0
          ? keepParts.reduce((acc, part) => acc.union(part))
          : null;
      // Emphasis works by subtraction: one anchor carries the soft halo,
      // its related subgraph keeps normal rendering and labels, and only the
      // unrelated remainder fades — no glow stacked across the neighborhood.
      const anchor = selectedNode ?? focusNode;
      instance.batch(() => {
        all.removeClass(["faded", "highlight", "focus-set", "selected-link"]);
        selectedNode?.connectedEdges().addClass("selected-link");
        if (keep) {
          if (anchor) anchor.addClass("highlight");
          keep.nodes().addClass("focus-set");
          all.not(keep).addClass("faded");
        }
      });
    };
    // Hover stays element-local (§6): one node gets a gentle accent stroke
    // plus its label, and at most its direct edges lift slightly. No
    // whole-graph fade, no React state, no stylesheet reapplication — moving
    // across the graph never rearranges the canvas brightness.
    let hoveredNode: NodeSingular | null = null;
    const clearHover = () => {
      if (!hoveredNode || hoveredNode.removed()) {
        hoveredNode = null;
        return;
      }
      instance.batch(() => {
        hoveredNode?.removeClass("hovered");
        hoveredNode?.connectedEdges().removeClass("hover-link");
      });
      hoveredNode = null;
    };
    instance.on("mouseover", "node", (event) => {
      const node = event.target;
      if (hoveredNode && hoveredNode.id() !== node.id()) clearHover();
      hoveredNode = node;
      instance.batch(() => {
        node.addClass("hovered");
        node.connectedEdges().addClass("hover-link");
      });
      if (container.current)
        container.current.title = String(node.data("fullTitle") || "");
    });
    instance.on("mouseout", "node", clearHover);
    // The user owns the viewport after any gesture: drop pending auto-fit.
    const dropSettleFit = () => {
      sim.cancelSettleFit();
      instance.stop(true, false);
    };
    instance.on("scrollzoom", dropSettleFit);
    instance.on("dragpan", dropSettleFit);
    instance.on("drag", "node", dropSettleFit);
    const resize = new ResizeObserver(() => instance.resize());
    resize.observe(container.current);
    return () => {
      clearTimeout(zoomTimer);
      detachWheel();
      resize.disconnect();
      sim.stop();
      locateRef.current = null;
      instance.destroy();
      graph.current = null;
      physics.current = null;
      applyVisuals.current = () => {};
      applyEmphasis.current = () => {};
    };
  }, []);
  // New data (polling, filters, focus) arrives as fresh references; sync
  // diffs elements by id and only reheats the layout on real changes.
  const firstSync = useRef(true);
  useEffect(() => {
    physics.current?.sync(graphElements(data, tRef.current("state.untitled")));
    closeEdgeDetails();
    // Newcomers need the current label/zoom tier applied.
    applyVisuals.current();
    // Frame the initial layout once it settles (the seeded cluster expands
    // far past its initial extent); later refreshes never touch the viewport.
    if (firstSync.current) {
      firstSync.current = false;
      physics.current?.settleFit();
    }
  }, [data]);
  useEffect(() => {
    labelModeRef.current = labelMode;
    applyVisuals.current();
  }, [labelMode]);
  // Edge display mode: all / strong-only / selected-neighborhood. Filtering is
  // display-only (classes); elements and physics stay untouched.
  useEffect(() => {
    const instance = graph.current;
    if (!instance) return;
    const edges = instance.edges();
    instance.batch(() => {
      edges.removeClass("mode-hidden");
      if (edgeMode === "strong") {
        edges.filter((edge) => edge.data("strong") !== "yes").addClass("mode-hidden");
      } else if (edgeMode === "selected") {
        const node = selected ? instance.getElementById(selected) : null;
        if (node && node.nonempty() && node.isNode()) {
          edges.not(node.connectedEdges()).addClass("mode-hidden");
        }
      }
    });
  }, [edgeMode, selected, data]);
  useEffect(() => {
    const instance = graph.current;
    if (!instance) return;
    instance.batch(() => {
      instance.edges(".inspected").removeClass("inspected");
      if (inspectedEdge) instance.getElementById(inspectedEdge).addClass("inspected");
    });
  }, [inspectedEdge, data]);
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
    closeEdgeDetails();
    instance.nodes().unselect();
    if (selected) instance.getElementById(selected).select();
    // Clicking a node highlights its one-hop neighborhood (§3 mode A).
    emphasisState.current.selected = selected;
    applyEmphasis.current();
  }, [selected, data]);
  // Local focus mode (§3 mode B): tighten springs, keep neighborhood labels
  // on, and emphasize the display-layer two-hop set when expanded. Only
  // classes and physics parameters change — the instance and viewport stay.
  useEffect(() => {
    const instance = graph.current;
    const active = !!localFocus;
    const wasActive = !!prevLocalFocus.current;
    prevLocalFocus.current = localFocus;
    emphasisState.current.focus = localFocus;
    emphasisState.current.hops = localHops;
    physics.current?.setTightLinks(active);
    applyEmphasis.current();
    applyVisuals.current();
    // Entering or leaving the local view changes the visible scope; reframe
    // once the adjusted layout settles (tight springs reshape the extent).
    if (instance && active !== wasActive) {
      physics.current?.settleFit();
    }
  }, [localFocus, localHops, data]);
  return (
    <div className="graph-frame">
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
            onClick={() => physics.current?.reLayout()}
          >
            <RotateCcw size={16} />
          </button>
        </div>
        <div className="graph-modes">
          <select
            aria-label={t("graph.viewMode")}
            title={t("graph.viewMode")}
            value={viewMode}
            onChange={(event) =>
              onViewMode(event.target.value as typeof viewMode)
            }
          >
            <option value="global">{t("graph.globalMode")}</option>
            <option value="focus" disabled={!canFocus}>
              {t("graph.focusOneHop")}
            </option>
            <option value="focus2" disabled={!canFocus}>
              {t("graph.focusTwoHops")}
            </option>
          </select>
          <select
            aria-label={t("graph.labelMode")}
            title={t("graph.labelMode")}
            value={labelMode}
            onChange={(event) =>
              setLabelMode(event.target.value as typeof labelMode)
            }
          >
            <option value="auto">{t("graph.labelsAuto")}</option>
            <option value="all">{t("graph.labelsAll")}</option>
            <option value="hidden">{t("graph.labelsHidden")}</option>
          </select>
          <select
            aria-label={t("graph.edgeMode")}
            title={t("graph.edgeMode")}
            value={edgeMode}
            onChange={(event) =>
              setEdgeMode(event.target.value as typeof edgeMode)
            }
          >
            <option value="all">{t("graph.edgesAll")}</option>
            <option value="strong">{t("graph.edgesStrong")}</option>
            <option value="selected">{t("graph.edgesSelected")}</option>
          </select>
        </div>
      </div>
      {relationship && (
        <div
          className="graph-relation-summary"
          role="status"
          aria-label={t("graph.relationshipDetails")}
        >
          <strong>{humanize(relationship.type)}</strong>
          <span
            className="graph-relation-names"
            title={`${relationship.source} → ${relationship.target}`}
          >
            {relationship.source} → {relationship.target}
          </span>
          <span>{t("graph.weight", { value: relationship.weight })}</span>
          <button
            className="icon-button"
            aria-label={t("graph.closeRelationship")}
            onClick={closeEdgeDetails}
          >
            <X size={14} />
          </button>
        </div>
      )}
    </div>
  );
}
export function GraphView({ meta, refresh }: { meta: Meta; refresh: number }) {
  const { route, navigate } = useRouter();
  const t = useT();
  const locateRef = useRef<((id: string) => void) | null>(null);
  const [relationship, setRelationship] = useState<GraphRelationship | null>(null);
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
  // §3 dual mode: the URL focus parameter drives the one-hop server focus;
  // the two-hop view is a display-layer expansion over the current dataset.
  const [twoHopFocus, setTwoHopFocus] = useState(false);
  useEffect(() => {
    // Back navigation and closing the preview both clear URL selection. Drop
    // the local expansion too so a later selection starts in global mode.
    if (!selectedID) setTwoHopFocus(false);
  }, [selectedID]);
  const viewMode: "global" | "focus" | "focus2" = focus
    ? "focus"
    : twoHopFocus && selectedID
      ? "focus2"
      : "global";
  function update(key: string, value: string) {
    const next = new URLSearchParams(route.search);
    if (value || key === "selected") next.set(key, value);
    else next.delete(key);
    if (key === "focus") next.delete("selected");
    navigate(routeURL(route.store, "graph", next.toString()));
  }
  const onViewMode = (mode: "global" | "focus" | "focus2") => {
    setTwoHopFocus(mode === "focus2");
    if (mode === "focus" && selectedID) update("focus", selectedID);
    else if (focus) update("focus", "");
  };
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
          <button
            className="button subtle"
            onClick={() => {
              setTwoHopFocus(false);
              update("focus", "");
            }}
          >
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
                    localFocus={focus || (twoHopFocus ? selectedID : "")}
                    localHops={focus ? 1 : 2}
                    viewMode={viewMode}
                    canFocus={!!selectedID}
                    onViewMode={onViewMode}
                    locateRef={locateRef}
                    relationship={relationship}
                    onInspectEdge={setRelationship}
                  />
                  {selected && (
                    <GraphPreview
                      memory={selected}
                      onClose={() => select("")}
                      onExpand={() => {
                        setTwoHopFocus(false);
                        update("focus", selected.id);
                      }}
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
                        onClick={() => {
                          select(node.id);
                          // Measure after the preview panel has opened, so the
                          // node is centered in the remaining canvas space.
                          requestAnimationFrame(() =>
                            locateRef.current?.(node.id),
                          );
                        }}
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
