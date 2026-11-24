import {
  forceCollide,
  forceLink,
  forceManyBody,
  forceSimulation,
  forceX,
  forceY,
  type SimulationLinkDatum,
  type SimulationNodeDatum,
} from "d3-force";
import type { Core, ElementDefinition, EdgeSingular, NodeSingular } from "cytoscape";

/**
 * Force-directed physics for the knowledge graph.
 *
 * d3-force owns the simulation (repulsion, springs, collisions, damping) while
 * Cytoscape keeps rendering, hit-testing and gesture events. The two share
 * element identity by id: every managed node keeps its Cytoscape element and
 * its simulation datum in sync, and each simulation tick writes positions back
 * through a single `cy.batch` call. Nothing here feeds React state per frame.
 */

// Starting values tuned for 100–200 node graphs (requirements §1, §8).
const CHARGE_STRENGTH = -240;
const CHARGE_DISTANCE_MAX = 500;
const CHARGE_THETA = 0.9;
const COLLIDE_PADDING = 8;
const CENTER_STRENGTH = 0.03;
const VELOCITY_DECAY = 0.32;
const ALPHA_DECAY = 0.028;
const ALPHA_MIN = 0.001;
const DRAG_ALPHA_TARGET = 0.3;
const RELEASE_ALPHA = 0.15;
const DATA_SYNC_ALPHA = 0.5;
const ISOLATED_RING_BASE = 140;
const ISOLATED_RING_STEP = 30;
const MIN_LINK_WEIGHT = 0.05;

export interface PhysicsElements {
  nodes: ElementDefinition[];
  edges: ElementDefinition[];
}

export interface GraphPhysics {
  /** Reconcile elements with new data by id; reheats only on real changes. */
  sync(elements: PhysicsElements): void;
  /** Re-seed every position and replay the layout from a hot start. */
  reLayout(): void;
  /** Fit the view once the running layout settles (initial load / reset). */
  settleFit(): void;
  /** Drop a pending settle-fit after the user takes over the viewport. */
  cancelSettleFit(): void;
  /** Tighten springs for local focus mode; reheats moderately on change. */
  setTightLinks(tight: boolean): void;
  onGrab(id: string): void;
  onDrag(id: string): void;
  onFree(id: string): void;
  stop(): void;
}

interface SimNode extends SimulationNodeDatum {
  id: string;
  size: number; // rendered diameter, mirrors the element's data(size)
}

interface SimLink extends SimulationLinkDatum<SimNode> {
  weight: number;
}

interface ManagedNode {
  ele: NodeSingular;
  sim: SimNode;
}

interface ManagedEdge {
  ele: EdgeSingular;
  source: SimNode;
  target: SimNode;
  weight: number;
}

interface DragWheelRenderer {
  hoverData?: {
    draggingEles?: boolean;
    dragging?: boolean;
    mdownPos?: number[];
  };
  selection?: unknown[];
  projectIntoViewport?(clientX: number, clientY: number): number[];
  data?: { wheelZooming?: boolean; bgActivePosistion?: { x: number; y: number } };
}

function nodeSize(def: ElementDefinition): number {
  const size = Number(def.data?.size);
  return Number.isFinite(size) && size > 0 ? size : 20;
}

function edgeWeight(def: ElementDefinition): number {
  const weight = Number(def.data?.weight);
  // Guard the 1/weight distance term against zero or missing weights.
  return Number.isFinite(weight) && weight > 0 ? weight : MIN_LINK_WEIGHT;
}

/**
 * Seed positions for a fresh layout: connected nodes follow the same
 * phyllotaxis spiral d3-force uses, while zero-degree nodes are scattered on a
 * ring around it so isolated memories never stack into a single line.
 */
function seedLayout(
  ids: string[],
  isolated: Set<string>,
): Map<string, { x: number; y: number }> {
  const positions = new Map<string, { x: number; y: number }>();
  let spiralIndex = 0;
  const ringIDs: string[] = [];
  for (const id of ids) {
    if (isolated.has(id)) {
      ringIDs.push(id);
      continue;
    }
    const radius = 10 * Math.sqrt(0.5 + spiralIndex);
    const angle = spiralIndex * 2.399963229728653; // golden angle, as in d3-force
    positions.set(id, { x: radius * Math.cos(angle), y: radius * Math.sin(angle) });
    spiralIndex += 1;
  }
  const ringRadius = ISOLATED_RING_BASE + ISOLATED_RING_STEP * Math.sqrt(ringIDs.length);
  ringIDs.forEach((id, index) => {
    const angle = (index / Math.max(1, ringIDs.length)) * Math.PI * 2 + Math.random() * 0.6;
    const radius = ringRadius * (0.75 + Math.random() * 0.5);
    positions.set(id, { x: radius * Math.cos(angle), y: radius * Math.sin(angle) });
  });
  return positions;
}

export function createGraphPhysics(cy: Core): GraphPhysics {
  const nodes = new Map<string, ManagedNode>();
  const edges = new Map<string, ManagedEdge>();
  const dragIDs = new Set<string>();
  let primed = false;
  // Local focus mode (§3 mode B) pulls related memories closer together.
  let tightLinks = false;

  const linkForce = forceLink<SimNode, SimLink>([])
    .id((node) => node.id)
    .distance((link) => (70 + 60 / link.weight) * (tightLinks ? 0.75 : 1))
    .strength((link) => {
      const base = Math.min(0.6, 0.12 + 0.3 * link.weight);
      return tightLinks ? Math.min(1, base * 1.4) : base;
    });

  const simulation = forceSimulation<SimNode>([])
    .force(
      "charge",
      forceManyBody<SimNode>().strength(CHARGE_STRENGTH).distanceMax(CHARGE_DISTANCE_MAX).theta(CHARGE_THETA),
    )
    .force("link", linkForce)
    .force(
      "collide",
      forceCollide<SimNode>()
        .radius((node) => node.size / 2 + COLLIDE_PADDING)
        .iterations(2),
    )
    .force("centerX", forceX(0).strength(CENTER_STRENGTH))
    .force("centerY", forceY(0).strength(CENTER_STRENGTH))
    .velocityDecay(VELOCITY_DECAY)
    .alphaDecay(ALPHA_DECAY)
    .alphaMin(ALPHA_MIN)
    .alpha(1)
    .stop();

  const writePositions = () => {
    cy.batch(() => {
      for (const [id, entry] of nodes) {
        if (dragIDs.has(id)) {
          // Cytoscape owns the dragged node's position; mirror it into the pin
          // so forces keep acting around the live cursor location.
          const live = entry.ele.position();
          entry.sim.fx = live.x;
          entry.sim.fy = live.y;
          continue;
        }
        const { x, y } = entry.sim;
        if (x != null && y != null) entry.ele.position({ x, y });
      }
    });
  };
  simulation.on("tick", writePositions);

  // Auto-framing waits for the layout to settle: fitting at seed positions
  // would frame a tiny cluster that the expanding simulation outgrows.
  let fitOnSettle = false;
  simulation.on("end", () => {
    if (!fitOnSettle) return;
    fitOnSettle = false;
    cy.fit(undefined, 55);
  });
  const settleFit = () => {
    fitOnSettle = true;
  };
  const cancelSettleFit = () => {
    fitOnSettle = false;
  };

  const sync = (elements: PhysicsElements) => {
    const wantNodes = new Map<string, ElementDefinition>();
    for (const def of elements.nodes) {
      const id = String(def.data?.id ?? "");
      if (id) wantNodes.set(id, def);
    }
    const wantEdges = new Map<string, ElementDefinition>();
    for (const def of elements.edges) {
      const id = String(def.data?.id ?? "");
      const source = String(def.data?.source ?? "");
      const target = String(def.data?.target ?? "");
      if (id && wantNodes.has(source) && wantNodes.has(target)) wantEdges.set(id, def);
    }

    // Degrees and adjacency decide how newcomers are seeded.
    const degree = new Map<string, number>();
    const adjacency = new Map<string, string[]>();
    const connect = (from: string, to: string) => {
      degree.set(from, (degree.get(from) || 0) + 1);
      const list = adjacency.get(from);
      if (list) list.push(to);
      else adjacency.set(from, [to]);
    };
    for (const def of wantEdges.values()) {
      const source = String(def.data?.source);
      const target = String(def.data?.target);
      connect(source, target);
      connect(target, source);
    }

    let centroid = { x: 0, y: 0 };
    let placed = 0;
    for (const entry of nodes.values()) {
      if (entry.sim.x != null && entry.sim.y != null) {
        centroid.x += entry.sim.x;
        centroid.y += entry.sim.y;
        placed += 1;
      }
    }
    if (placed > 0) centroid = { x: centroid.x / placed, y: centroid.y / placed };

    const seedNewcomer = (id: string): { x: number; y: number } => {
      if ((degree.get(id) || 0) > 0) {
        for (const neighborID of adjacency.get(id) || []) {
          const neighbor = nodes.get(neighborID)?.sim;
          if (neighbor && neighbor.x != null && neighbor.y != null) {
            return {
              x: neighbor.x + (Math.random() - 0.5) * 40,
              y: neighbor.y + (Math.random() - 0.5) * 40,
            };
          }
        }
        const radius = 10 * Math.sqrt(0.5 + nodes.size);
        const angle = Math.random() * Math.PI * 2;
        return { x: centroid.x + radius * Math.cos(angle), y: centroid.y + radius * Math.sin(angle) };
      }
      // Zero-degree: scatter on a ring around the existing layout.
      const radius = ISOLATED_RING_BASE + Math.random() * ISOLATED_RING_STEP * 4;
      const angle = Math.random() * Math.PI * 2;
      return { x: centroid.x + radius * Math.cos(angle), y: centroid.y + radius * Math.sin(angle) };
    };

    let changed = false;
    cy.batch(() => {
      for (const [id, entry] of nodes) {
        if (!wantNodes.has(id)) {
          entry.ele.remove();
          nodes.delete(id);
          dragIDs.delete(id);
          changed = true;
        }
      }
      for (const [id, entry] of edges) {
        if (!wantEdges.has(id)) {
          entry.ele.remove();
          edges.delete(id);
          changed = true;
        }
      }
      for (const [id, def] of wantNodes) {
        const data = def.data || {};
        const existing = nodes.get(id);
        if (!existing) {
          const sim: SimNode = { id, size: nodeSize(def) };
          const position = seedNewcomer(id);
          sim.x = position.x;
          sim.y = position.y;
          nodes.set(id, { ele: cy.add({ ...def, position }) as unknown as NodeSingular, sim });
          changed = true;
          continue;
        }
        const patch: Record<string, unknown> = {};
        for (const key of Object.keys(data)) {
          if (key !== "id" && existing.ele.data(key) !== data[key]) patch[key] = data[key];
        }
        if (Object.keys(patch).length > 0) {
          existing.ele.data(patch);
          if (patch.size !== undefined) existing.sim.size = nodeSize(def);
          changed = true;
        }
      }
      for (const [id, def] of wantEdges) {
        const data = def.data || {};
        const existing = edges.get(id);
        if (!existing) {
          const source = nodes.get(String(data.source))?.sim;
          const target = nodes.get(String(data.target))?.sim;
          if (source && target) {
            edges.set(id, {
              ele: cy.add(def) as unknown as EdgeSingular,
              source,
              target,
              weight: edgeWeight(def),
            });
            changed = true;
          }
          continue;
        }
        const patch: Record<string, unknown> = {};
        for (const key of Object.keys(data)) {
          if (
            key !== "id" &&
            key !== "source" &&
            key !== "target" &&
            existing.ele.data(key) !== data[key]
          ) {
            patch[key] = data[key];
          }
        }
        if (Object.keys(patch).length > 0) {
          existing.ele.data(patch);
          existing.weight = edgeWeight(def);
          changed = true;
        }
      }
    });

    // Polling that returns identical data must not disturb the settled layout.
    if (!changed) return;
    pushLinks();
    simulation.nodes([...nodes.values()].map((entry) => entry.sim));
    // First sync lays the graph out from cold (alpha 1); later real changes
    // only reheat (alpha 0.5) so the update stays progressive.
    simulation.alpha(primed ? DATA_SYNC_ALPHA : 1).restart();
    primed = true;
  };

  // (Re)bind the current edges; re-setting links recomputes the per-link
  // distance/strength constants so tuning changes apply immediately.
  const pushLinks = () => {
    linkForce.links(
      [...edges.values()].map((edge) => ({
        source: edge.source,
        target: edge.target,
        weight: edge.weight,
      })),
    );
  };

  const setTightLinks = (tight: boolean) => {
    if (tight === tightLinks) return;
    tightLinks = tight;
    pushLinks();
    // Moderate reheat only: the layout tightens progressively.
    simulation.alpha(0.4).restart();
  };

  const reLayout = () => {
    const degree = new Map<string, number>();
    for (const edge of edges.values()) {
      degree.set(edge.source.id, (degree.get(edge.source.id) || 0) + 1);
      degree.set(edge.target.id, (degree.get(edge.target.id) || 0) + 1);
    }
    const ids = [...nodes.keys()];
    const isolated = new Set(ids.filter((id) => !degree.get(id)));
    const positions = seedLayout(ids, isolated);
    for (const [id, entry] of nodes) {
      const position = positions.get(id);
      if (!position) continue;
      entry.sim.x = position.x;
      entry.sim.y = position.y;
      entry.sim.vx = 0;
      entry.sim.vy = 0;
      entry.sim.fx = null;
      entry.sim.fy = null;
    }
    dragIDs.clear();
    simulation.alphaTarget(0).alpha(1).restart();
    writePositions();
    // Framing happens once the replayed layout settles, not at the seed
    // positions (the graph expands far past the seeded extent).
    settleFit();
  };

  const onGrab = (id: string) => {
    const entry = nodes.get(id);
    if (!entry) return;
    const live = entry.ele.position();
    entry.sim.fx = live.x;
    entry.sim.fy = live.y;
    dragIDs.add(id);
    simulation.alphaTarget(DRAG_ALPHA_TARGET).restart();
  };

  const onDrag = (id: string) => {
    const entry = nodes.get(id);
    if (!entry || !dragIDs.has(id)) return;
    const live = entry.ele.position();
    entry.sim.fx = live.x;
    entry.sim.fy = live.y;
    if (simulation.alphaTarget() !== DRAG_ALPHA_TARGET) {
      simulation.alphaTarget(DRAG_ALPHA_TARGET).restart();
    }
  };

  const onFree = (id: string) => {
    const entry = nodes.get(id);
    if (!entry) return;
    // User placement: the drop position IS the pin (fx/fy stay set). The
    // released node is a fixed anchor for every later simulation run —
    // springs, repulsion and reheats move the rest of the network around it,
    // never the node itself. Only reLayout() clears pins deliberately.
    dragIDs.delete(id);
    if (dragIDs.size > 0) return;
    // Release: cool down; the neighbors rebound around the new anchor.
    simulation.alphaTarget(0);
    if (simulation.alpha() < RELEASE_ALPHA) simulation.alpha(RELEASE_ALPHA);
    simulation.restart();
  };

  return {
    sync,
    reLayout,
    setTightLinks,
    settleFit,
    cancelSettleFit,
    onGrab,
    onDrag,
    onFree,
    stop: () => simulation.stop(),
  };
}

// A calibrated mouse notch changes zoom by ~12%, so small overviews can be
// explored without dozens of wheel steps. Share it with the drag takeover.
export const GRAPH_WHEEL_SENSITIVITY = 4;
const WHEEL_SAMPLE_COUNT = 4;

/**
 * Cytoscape's wheel handler refuses to zoom while a drag gesture is active
 * ("if pan dragging ... wheel movements make no zoom"), which is why scrolling
 * over a held node does nothing. This companion listener runs after
 * Cytoscape's own handler on the same container and takes over exactly there:
 * when the event was consumed (defaultPrevented) but a drag state is active,
 * it applies the zoom with the upstream math, anchored at the cursor so the
 * dragged node stays under the pointer and the gesture survives. Wheel events
 * outside drag gestures keep going through Cytoscape untouched.
 */
export function attachDragWheelZoom(
  cy: Core,
  container: HTMLElement,
  options: { wheelSensitivity: number; isDragging: () => boolean },
): () => void {
  // Mirror of Cytoscape's wheel-delta sampling so the takeover zooms at the
  // same speed as normal scrolling (initial clamp, then device compensation).
  let deltas: number[] = [];
  let inaccurate: boolean | null = null;
  let inaccurateFactor = 100000;
  let wheelTimeout: ReturnType<typeof setTimeout> | undefined;

  const renderer = () =>
    (cy as unknown as { renderer?: () => DragWheelRenderer }).renderer?.();

  const onWheel = (event: WheelEvent) => {
    let clamp = false;
    let delta = event.deltaY;
    if (delta == null) {
      const legacy = event as WheelEvent & { wheelDeltaY?: number; wheelDelta?: number };
      if (legacy.wheelDeltaY != null) delta = legacy.wheelDeltaY / 4;
      else if (legacy.wheelDelta != null) delta = legacy.wheelDelta / 4;
    }
    if (inaccurate === null) {
      if (deltas.length >= WHEEL_SAMPLE_COUNT) {
        inaccurate = deltas.every((value) => value % 5 === 0);
        if (!inaccurate) {
          const first = Math.abs(deltas[0]);
          inaccurate = deltas.every((value) => Math.abs(value) === first) && first > 5;
        }
        if (inaccurate) {
          for (const value of deltas) inaccurateFactor = Math.min(Math.abs(value), inaccurateFactor);
        }
      } else {
        deltas.push(delta);
        clamp = true;
      }
    } else if (inaccurate) {
      inaccurateFactor = Math.min(Math.abs(delta), inaccurateFactor);
    }

    // Take over only when Cytoscape consumed the wheel but refused to zoom.
    if (!event.defaultPrevented) return;
    const hover = renderer()?.hoverData;
    if (!(options.isDragging() || hover?.draggingEles || hover?.dragging)) return;
    if (
      !cy.panningEnabled?.() ||
      !cy.userPanningEnabled?.() ||
      !cy.zoomingEnabled?.() ||
      !cy.userZoomingEnabled?.()
    ) {
      return;
    }

    const r = renderer();
    const zoom = cy.zoom();
    let renderedX: number;
    let renderedY: number;
    // Called as a member expression so the renderer stays bound as `this`.
    const projected =
      r && r.projectIntoViewport
        ? r.projectIntoViewport(event.clientX, event.clientY)
        : undefined;
    if (projected) {
      const pan = cy.pan();
      renderedX = projected[0] * zoom + pan.x;
      renderedY = projected[1] * zoom + pan.y;
    } else {
      const rect = container.getBoundingClientRect();
      renderedX = event.clientX - rect.left;
      renderedY = event.clientY - rect.top;
    }
    // From here on the math mirrors Cytoscape's wheelHandler exactly.
    if (clamp && Math.abs(delta) > 5) delta = Math.sign(delta) * 5;
    let diff = delta / -250;
    if (inaccurate) {
      diff /= inaccurateFactor;
      diff *= 3;
    }
    diff = diff * options.wheelSensitivity;
    if (event.deltaMode === 1) diff *= 33;
    cy.zoom({
      level: zoom * Math.pow(10, diff),
      renderedPosition: { x: renderedX, y: renderedY },
    });
    cy.emit("scrollzoom");
    if (r?.data) {
      r.data.wheelZooming = true;
      clearTimeout(wheelTimeout);
      wheelTimeout = setTimeout(() => {
        if (r.data) r.data.wheelZooming = false;
      }, 150);
    }
    // An in-flight background pan keeps model-space cursor state; refresh it
    // so the next pan increment does not jump across the new viewport. The
    // first increment after mousedown compares against the mousedown
    // projection (hoverData.mdownPos), so that anchor needs the same refresh.
    const selection = r?.selection;
    if (Array.isArray(selection) && r?.projectIntoViewport) {
      const now = r.projectIntoViewport(event.clientX, event.clientY);
      selection[2] = now[0];
      selection[3] = now[1];
      const mdownPos = r.hoverData?.mdownPos;
      if (Array.isArray(mdownPos)) {
        mdownPos[0] = now[0];
        mdownPos[1] = now[1];
        const indicator = r.data?.bgActivePosistion;
        if (indicator) {
          indicator.x = now[0];
          indicator.y = now[1];
        }
      }
    }
  };

  container.addEventListener("wheel", onWheel, { capture: true, passive: true });
  return () => {
    container.removeEventListener("wheel", onWheel, { capture: true });
    clearTimeout(wheelTimeout);
  };
}
