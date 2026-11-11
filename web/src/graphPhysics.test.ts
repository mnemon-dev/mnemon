import cytoscape from "cytoscape";
import type { Core, ElementDefinition } from "cytoscape";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import type { GraphPhysics } from "./graphPhysics";

/**
 * Regression coverage for the physics layer and the drag/wheel fix, running
 * the real Cytoscape instance (no cytoscape mock) under jsdom. Event flows
 * mirror the manual verification of Cytoscape 3.32.0 load-listeners behavior.
 */

let physicsModule: typeof import("./graphPhysics");

let harnesses: { cy: Core; physics: GraphPhysics }[] = [];

beforeAll(async () => {
  // jsdom ships no canvas implementation; give Cytoscape a no-op 2d context.
  window.HTMLCanvasElement.prototype.getContext = function (
    this: HTMLCanvasElement,
  ) {
    const canvas = this;
    return new Proxy(
      {},
      {
        get: (_target, key) => {
          if (key === "canvas") return canvas;
          if (key === "measureText") return () => ({ width: 10 });
          return () => {};
        },
        set: () => true,
      },
    );
  } as unknown as typeof window.HTMLCanvasElement.prototype.getContext;
  // d3-timer binds requestAnimationFrame at import time; install a
  // setTimeout-backed shim first so simulation frames schedule through the
  // (replaceable) global setTimeout, which fake timers can then drive
  // deterministically in the drag-response test. Cytoscape keeps working
  // because the shim still behaves like requestAnimationFrame.
  window.requestAnimationFrame = ((callback: FrameRequestCallback) =>
    setTimeout(() => callback(performance.now()), 16)) as typeof requestAnimationFrame;
  physicsModule = await import("./graphPhysics");
});

beforeEach(() => {
  // The shared setup restores real timers after every test, so install them
  // per test (before anything schedules simulation frames).
  vi.useFakeTimers({
    toFake: ["setTimeout", "clearTimeout", "setInterval", "clearInterval"],
  });
});

afterEach(() => {
  // Stop simulations before tearing down instances, or a scheduled tick can
  // fire against a destroyed renderer.
  for (const { cy, physics } of harnesses) {
    physics.stop();
    cy.destroy();
  }
  harnesses = [];
  // Flush any frame d3-timer still thinks is pending so the next test
  // starts with a clean scheduler.
  vi.advanceTimersByTime(50);
});

interface Harness {
  container: HTMLDivElement;
  cy: Core;
  physics: GraphPhysics;
  canvas: HTMLCanvasElement;
}

function createHarness(): Harness {
  const container = document.createElement("div");
  container.style.width = "800px";
  container.style.height = "600px";
  document.body.appendChild(container);
  container.getBoundingClientRect = () =>
    ({
      left: 0,
      top: 0,
      right: 800,
      bottom: 600,
      width: 800,
      height: 600,
      x: 0,
      y: 0,
      toJSON: () => {},
    }) as DOMRect;
  const cy = cytoscape({
    container,
    elements: [],
    layout: { name: "preset" },
    minZoom: 0.15,
    maxZoom: 3,
    wheelSensitivity: physicsModule.GRAPH_WHEEL_SENSITIVITY,
  });
  const physics = physicsModule.createGraphPhysics(cy);
  // Mirror the GraphCanvas wiring so drag gestures drive the simulation.
  cy.on("grab", "node", (event) => physics.onGrab(event.target.id()));
  cy.on("drag", "node", (event) => physics.onDrag(event.target.id()));
  cy.on("free", "node", (event) => physics.onFree(event.target.id()));
  harnesses.push({ cy, physics });
  cy.resize();
  resetViewport(cy);
  pinContainerCoords(cy);
  const canvas = container.querySelector("canvas") as HTMLCanvasElement;
  return { container, cy, physics, canvas };
}

function resetViewport(cy: Core) {
  // Zoom+pan reset (anchor is irrelevant once pan is zeroed).
  cy.zoom({ level: 1, renderedPosition: { x: 400, y: 300 } });
  cy.pan({ x: 0, y: 0 });
}

// jsdom has no layout, and Cytoscape's render loop re-derives container coords
// from clientWidth (0) whenever the pixel ratio check fires, so re-pin the
// cached coords after advancing timers.
function pinContainerCoords(cy: Core) {
  (cy as unknown as { renderer: () => { containerBB: unknown[] } })
    .renderer()
    .containerBB.splice(0, 5, 0, 0, 800, 600, 1);
}

function fire(
  target: Window | HTMLCanvasElement,
  type: "mousedown" | "mousemove" | "mouseup",
  props: { clientX: number; clientY: number },
) {
  const event = new MouseEvent(type, {
    bubbles: true,
    cancelable: true,
    clientX: props.clientX,
    clientY: props.clientY,
    button: 0,
    buttons: 1,
  });
  // jsdom omits the legacy `which` field Cytoscape checks for left clicks.
  Object.defineProperty(event, "which", { value: 1 });
  target.dispatchEvent(event);
}

function fireWheel(
  canvas: HTMLCanvasElement,
  props: { clientX: number; clientY: number; deltaY: number },
) {
  canvas.dispatchEvent(
    new WheelEvent("wheel", {
      bubbles: true,
      cancelable: true,
      clientX: props.clientX,
      clientY: props.clientY,
      deltaY: props.deltaY,
    }),
  );
}

const node = (id: string): ElementDefinition => ({
  data: { id, label: id, color: "#1f6feb", size: 25 },
});
const edge = (
  id: string,
  source: string,
  target: string,
  weight = 0.5,
): ElementDefinition => ({
  data: { id, source, target, label: "semantic", weight, supersedes: "no" },
});

// Scale fixture: `count` nodes with ~`edgesPer` edges each (acceptance 14).
function bigGraph(count: number, edgesPer: number): PhysicsElementsLike {
  const nodes = Array.from({ length: count }, (_, i) => node(`n${i}`));
  const edges: ElementDefinition[] = [];
  for (let i = 0; i < count; i++)
    for (let k = 1; k <= edgesPer; k++) {
      const j = (i + k * 7 + 3) % count;
      if (i !== j)
        edges.push(
          edge(`be-${i}-${k}`, `n${i}`, `n${j}`, 0.3 + ((i + k) % 5) * 0.15),
        );
    }
  return { nodes, edges };
}
type PhysicsElementsLike = {
  nodes: ElementDefinition[];
  edges: ElementDefinition[];
};

describe("graph physics element sync", () => {
  it("seeds isolated nodes off a straight line and diffs without churn", () => {
    const { cy, physics } = createHarness();
    const elements = {
      nodes: ["a", "b", "c", "d", "i1", "i2", "i3", "i4"].map((id) => node(id)),
      edges: [
        edge("e1", "a", "b"),
        edge("e2", "b", "c"),
        edge("e3", "c", "d"),
        edge("e4", "d", "a", 0.9),
      ],
    };
    physics.sync(elements);
    physics.stop();
    expect(cy.nodes().length).toBe(8);
    expect(cy.edges().length).toBe(4);

    // The old cose layout stacked isolated nodes on one horizontal line.
    const isolatedY = [1, 2, 3, 4].map((k) =>
      cy.getElementById(`i${k}`).position().y,
    );
    const isolatedX = [1, 2, 3, 4].map((k) =>
      cy.getElementById(`i${k}`).position().x,
    );
    expect(new Set(isolatedY.map((y) => Math.round(y * 1e6))).size).toBe(4);
    expect(new Set(isolatedX.map((x) => Math.round(x * 1e6))).size).toBe(4);

    // Identical data (fresh reference from polling) must not touch elements.
    let adds = 0;
    let removes = 0;
    cy.on("add", () => (adds += 1));
    cy.on("remove", () => (removes += 1));
    const before = { ...cy.getElementById("a").position() };
    physics.sync({
      nodes: elements.nodes.map((def) => ({
        data: { ...def.data, label: String(def.data?.label) },
      })),
      edges: elements.edges.map((def) => ({ data: { ...def.data } })),
    });
    physics.stop();
    expect(adds).toBe(0);
    expect(removes).toBe(0);
    const after = { ...cy.getElementById("a").position() };
    expect(after).toEqual(before);

    // A real change adds/removes by id and seeds newcomers near neighbors.
    physics.sync({
      nodes: elements.nodes.filter((def) => def.data?.id !== "i4").concat(node("e")),
      edges: elements.edges.concat(edge("e5", "a", "e")),
    });
    physics.stop();
    const ids = cy.nodes().map((n) => n.id());
    expect(ids).toContain("e");
    expect(ids).not.toContain("i4");
    const a = cy.getElementById("a").position();
    const e = cy.getElementById("e").position();
    expect(Math.abs(e.x - a.x)).toBeLessThanOrEqual(20.01);
    expect(Math.abs(e.y - a.y)).toBeLessThanOrEqual(20.01);

    // Data-only updates rewrite fields without moving nodes.
    const aBefore = { ...cy.getElementById("a").position() };
    physics.sync({
      nodes: elements.nodes
        .filter((def) => def.data?.id !== "i4")
        .concat(node("e"))
        .map((def) =>
          def.data?.id === "a" ? { data: { ...def.data, color: "#ff0000" } } : def,
        ),
      edges: elements.edges.concat(edge("e5", "a", "e")),
    });
    physics.stop();
    expect(cy.getElementById("a").data("color")).toBe("#ff0000");
    expect({ ...cy.getElementById("a").position() }).toEqual(aBefore);
  });
});

describe("graph physics drag response", () => {
  it("drags reheat the network and release settles to a stop", () => {
    const { cy, physics, canvas } = createHarness();
    physics.sync({
      nodes: ["a", "b", "c", "d", "w", "x", "y"].map((id) => node(id)),
      edges: [
        edge("e1", "a", "b"),
        edge("e2", "b", "c"),
        edge("e3", "c", "d"),
        edge("e4", "w", "x"),
        edge("e5", "x", "y"),
      ],
    });
    vi.advanceTimersByTime(5000); // let the initial layout settle
    pinContainerCoords(cy);
    // The settled layout sits around the origin; bring it into the canvas so
    // pointer events land inside the container.
    cy.pan({ x: 400, y: 300 });

    const a = cy.getElementById("a");
    const b = cy.getElementById("b");
    const start = { ...a.position() };
    const bBefore = { ...b.position() };

    // Grab and drag node a to the right by 80 rendered pixels.
    const grab = a.renderedPosition();
    fire(canvas, "mousedown", { clientX: grab.x, clientY: grab.y });
    fire(window, "mousemove", { clientX: grab.x + 80, clientY: grab.y });
    vi.advanceTimersByTime(300);
    pinContainerCoords(cy);
    expect(a.grabbed()).toBe(true);
    // The dragged node follows the cursor (zoom 1: model delta = 80).
    expect(a.position().x).toBeCloseTo(start.x + 80, 1);
    // Neighbors respond to the forces while dragging.
    const bDuring = { ...b.position() };
    expect(
      Math.hypot(bDuring.x - bBefore.x, bDuring.y - bBefore.y),
    ).toBeGreaterThan(0.5);

    // Release: the network rebounds and cools down to a full stop.
    fire(window, "mouseup", { clientX: grab.x + 80, clientY: grab.y });
    expect(a.grabbed()).toBe(false);
    vi.advanceTimersByTime(8000);
    const settled = cy.nodes().map((n) => ({ ...n.position() }));
    vi.advanceTimersByTime(2000);
    const later = cy.nodes().map((n) => ({ ...n.position() }));
    expect(later).toEqual(settled);
  });

  it("pins the release position: reheats, syncs and other drags never pull it back", () => {
    const { cy, physics, canvas } = createHarness();
    physics.sync({
      nodes: ["a", "b", "c", "d"].map((id) => node(id)),
      edges: [
        edge("e1", "a", "b", 0.8),
        edge("e2", "b", "c", 0.6),
        edge("e3", "c", "d", 0.7),
      ],
    });
    vi.advanceTimersByTime(6000); // settle
    pinContainerCoords(cy);
    cy.pan({ x: 400, y: 300 });

    // Drag node a far outside its cluster and release it there.
    const a = cy.getElementById("a");
    const grab = a.renderedPosition();
    const dropClient = { x: grab.x + 160, y: grab.y - 110 };
    fire(canvas, "mousedown", { clientX: grab.x, clientY: grab.y });
    fire(window, "mousemove", { clientX: dropClient.x, clientY: dropClient.y });
    const drop = { ...a.position() };
    fire(window, "mouseup", { clientX: dropClient.x, clientY: dropClient.y });
    vi.advanceTimersByTime(8000); // neighbors rebound and cool down
    expect(a.position().x).toBeCloseTo(drop.x, 6);
    expect(a.position().y).toBeCloseTo(drop.y, 6);

    // Real data changes reheat the simulation (alpha 0.5): pin survives.
    physics.sync({
      nodes: ["a", "b", "c", "d", "e"].map((id) => node(id)),
      edges: [
        edge("e1", "a", "b", 0.8),
        edge("e2", "b", "c", 0.6),
        edge("e3", "c", "d", 0.7),
        edge("e4", "d", "e", 0.5),
      ],
    });
    vi.advanceTimersByTime(8000);
    expect(a.position().x).toBeCloseTo(drop.x, 6);

    // Focus-mode spring reheats: pin survives.
    physics.setTightLinks(true);
    vi.advanceTimersByTime(6000);
    physics.setTightLinks(false);
    vi.advanceTimersByTime(6000);
    expect(a.position().x).toBeCloseTo(drop.x, 6);

    // Dragging another node must not move the pinned one.
    pinContainerCoords(cy);
    const b = cy.getElementById("b");
    const bGrab = b.renderedPosition();
    fire(canvas, "mousedown", { clientX: bGrab.x, clientY: bGrab.y });
    fire(window, "mousemove", { clientX: bGrab.x - 120, clientY: bGrab.y });
    fire(window, "mouseup", { clientX: bGrab.x - 120, clientY: bGrab.y });
    vi.advanceTimersByTime(6000);
    expect(a.position().x).toBeCloseTo(drop.x, 6);
    expect(a.position().y).toBeCloseTo(drop.y, 6);

    // Only the explicit reset recomputes: reLayout clears pins and reseeds.
    physics.reLayout();
    vi.advanceTimersByTime(9000);
    expect(
      Math.hypot(a.position().x - drop.x, a.position().y - drop.y),
    ).toBeGreaterThan(20);
  });
});

describe("performance and cooldown (§8, acceptance 14/15)", () => {
  it("keeps 200-node ticks cheap and stops entirely after cooldown", () => {
    const { cy, physics } = createHarness();
    physics.sync(bigGraph(200, 3));
    vi.advanceTimersByTime(8000); // initial layout settles

    // Reheat and measure the per-tick compute cost (forces + the single
    // cy.batch position write for 200 nodes). Canvas rasterisation is not
    // part of jsdom, so this measures the simulation + write path only.
    physics.setTightLinks(true);
    const ticks = 96; // 1600ms of 16.6ms frames
    const started = performance.now();
    vi.advanceTimersByTime(1600);
    const perTick = (performance.now() - started) / ticks;
    expect(perTick).toBeLessThan(10); // ms; well under a 60FPS frame budget

    // Cooldown: the only remaining timer is Cytoscape's render loop; the
    // simulation stops scheduling, nothing accumulates, positions freeze.
    physics.setTightLinks(false);
    vi.advanceTimersByTime(10000);
    const timersAfterCooldown = vi.getTimerCount();
    const frozen = cy.nodes().map((n) => ({ ...n.position() }));
    vi.advanceTimersByTime(3000);
    expect(vi.getTimerCount()).toBe(timersAfterCooldown);
    expect(cy.nodes().map((n) => ({ ...n.position() }))).toEqual(frozen);
  });
});

describe("settle framing (review fix: fit after convergence, not at seeds)", () => {
  it("fits only after the layout settles and honors user cancellation", () => {
    const { cy, physics } = createHarness();
    const fitSpy = vi.spyOn(cy, "fit");
    physics.sync({
      nodes: [node("a"), node("b"), node("c")],
      edges: [edge("e1", "a", "b", 0.8), edge("e2", "b", "c", 0.5)],
    });
    expect(fitSpy).not.toHaveBeenCalled(); // sync itself never frames
    // Reset: still no fit at the re-seeded positions...
    physics.reLayout();
    expect(fitSpy).not.toHaveBeenCalled();
    // ...only once the replayed layout has fully settled.
    vi.advanceTimersByTime(12000);
    expect(fitSpy).toHaveBeenCalledTimes(1);
    // A canceled settle-fit never frames the view.
    fitSpy.mockClear();
    physics.settleFit();
    physics.cancelSettleFit();
    vi.advanceTimersByTime(12000);
    expect(fitSpy).not.toHaveBeenCalled();
  });
});

describe("local focus spring tuning", () => {
  it("tightens springs in local focus mode and relaxes them on exit", () => {
    const { cy, physics } = createHarness();
    physics.sync({
      nodes: [node("a"), node("b"), node("c")],
      edges: [edge("e1", "a", "b", 0.8), edge("e2", "b", "c", 0.8)],
    });
    vi.advanceTimersByTime(6000); // settle at normal spring length
    const distance = () => {
      const a = cy.getElementById("a").position();
      const b = cy.getElementById("b").position();
      return Math.hypot(a.x - b.x, a.y - b.y);
    };
    const globalDistance = distance();
    physics.setTightLinks(true);
    vi.advanceTimersByTime(6000);
    const focusedDistance = distance();
    expect(focusedDistance).toBeLessThan(globalDistance - 10);
    physics.setTightLinks(false);
    vi.advanceTimersByTime(6000);
    expect(distance()).toBeGreaterThan(focusedDistance + 10);
  });
});

describe("wheel zoom during drag gestures", () => {
  it.each([100, 120])("zooms a small overview to readable scale in twelve mouse-wheel steps (delta %s)", (deltaY) => {
    const { cy, canvas } = createHarness();
    cy.zoom({ level: 0.2, renderedPosition: { x: 400, y: 300 } });
    const cursorModelPosition = () => ({
      x: (400 - cy.pan().x) / cy.zoom(),
      y: (300 - cy.pan().y) / cy.zoom(),
    });
    const anchor = cursorModelPosition();
    for (let step = 0; step < 12; step++)
      fireWheel(canvas, { clientX: 400, clientY: 300, deltaY: -deltaY });
    expect(cy.zoom()).toBeGreaterThanOrEqual(0.9);
    expect(cy.zoom()).toBeLessThan(1.3);
    expect(cursorModelPosition().x).toBeCloseTo(anchor.x, 6);
    expect(cursorModelPosition().y).toBeCloseTo(anchor.y, 6);
    for (let step = 0; step < 12; step++)
      fireWheel(canvas, { clientX: 400, clientY: 300, deltaY });
    expect(cy.zoom()).toBeLessThanOrEqual(0.3);
    expect(cy.zoom()).toBeGreaterThanOrEqual(0.15);
  });

  it("keeps zooming while a node is dragged without jumps or lost gestures", () => {
    const { container, cy, physics, canvas } = createHarness();
    physics.sync({
      nodes: [node("a"), node("b")],
      edges: [edge("e1", "a", "b")],
    });
    physics.stop();
    // Deterministic geometry: model (400,300) renders at (400,300) at zoom 1.
    cy.getElementById("a").position({ x: 400, y: 300 });
    cy.getElementById("b").position({ x: 100, y: 100 });
    const a = cy.getElementById("a");

    let dragging = false;
    let freeCount = 0;
    let dragfreeCount = 0;
    cy.on("drag", "node", () => (dragging = true));
    cy.on("free", "node", () => (dragging = false, freeCount += 1));
    a.on("dragfree", () => (dragfreeCount += 1));
    const detach = physicsModule.attachDragWheelZoom(cy, container, {
      wheelSensitivity: physicsModule.GRAPH_WHEEL_SENSITIVITY,
      isDragging: () => dragging,
    });

    // 1) Idle wheel: Cytoscape zooms; the guard must not double-apply.
    // First events are clamped to |delta|<=5 while deltas are sampled,
    // so one notch is 10^(5/250*4).
    const single = Math.pow(10, 0.08);
    fireWheel(canvas, { clientX: 400, clientY: 300, deltaY: -100 });
    expect(cy.zoom()).toBeCloseTo(single, 6);
    resetViewport(cy);

    // 2) Holding without moving still zooms normally (upstream path).
    fire(canvas, "mousedown", { clientX: 400, clientY: 300 });
    fireWheel(canvas, { clientX: 420, clientY: 300, deltaY: -100 });
    expect(cy.zoom()).toBeCloseTo(single, 6);
    resetViewport(cy);
    fire(window, "mouseup", { clientX: 400, clientY: 300 });
    expect(freeCount).toBeGreaterThanOrEqual(1);

    // 3) The fix: drag the node, then wheel.
    fire(canvas, "mousedown", { clientX: 400, clientY: 300 });
    fire(window, "mousemove", { clientX: 420, clientY: 300 });
    expect(dragging).toBe(true);
    const beforeZoom = cy.zoom();
    const before = { ...a.position() };
    fireWheel(canvas, { clientX: 420, clientY: 300, deltaY: -100 });
    expect(cy.zoom()).toBeGreaterThan(beforeZoom); // zoom happened mid-drag
    expect(cy.zoom() / beforeZoom).toBeCloseTo(single, 6);
    expect(a.grabbed()).toBe(true); // gesture survived
    expect({ ...a.position() }).toEqual(before); // cursor anchor: no model jump
    // Same cursor position after zooming: no movement, no jump.
    fire(window, "mousemove", { clientX: 420, clientY: 300 });
    expect({ ...a.position() }).toEqual(before);
    // Moving on: the model delta converts through the new zoom exactly.
    fire(window, "mousemove", { clientX: 432, clientY: 300 });
    const moved = { ...a.position() };
    expect(moved.x - before.x).toBeCloseTo(12 / cy.zoom(), 5);

    // 4) Release restores the normal wheel path.
    fire(window, "mouseup", { clientX: 432, clientY: 300 });
    expect(dragfreeCount).toBe(1);
    const afterRelease = cy.zoom();
    fireWheel(canvas, { clientX: 400, clientY: 300, deltaY: -100 });
    expect(cy.zoom()).toBeGreaterThan(afterRelease);

    // 5) Background pan: upstream blocks wheel here too; the guard takes
    // over and the following pan increment stays jump-free.
    resetViewport(cy);
    fire(canvas, "mousedown", { clientX: 600, clientY: 500 }); // empty space
    fire(window, "mousemove", { clientX: 620, clientY: 500 });
    const panBefore = { ...cy.pan() };
    const panWheelZoom = cy.zoom();
    fireWheel(canvas, { clientX: 620, clientY: 500, deltaY: -100 });
    expect(cy.zoom()).toBeGreaterThan(panWheelZoom);
    const panAfterWheel = cy.pan().x;
    fire(window, "mousemove", { clientX: 630, clientY: 500 });
    const panAfter = { ...cy.pan() };
    // The wheel zoom itself shifts pan (cursor anchor); the following pan
    // increment must equal the pure cursor delta of 10px — no jump.
    expect(panAfter.x - panAfterWheel).toBeCloseTo(10, 5);
    fire(window, "mouseup", { clientX: 630, clientY: 500 });

    detach();
  });
});
