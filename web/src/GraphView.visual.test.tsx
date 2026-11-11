import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { Profiler } from "react";
import { beforeAll, expect, it, vi } from "vitest";
import type { Core, EdgeSingular, NodeSingular } from "cytoscape";
import { GraphView } from "./GraphView";
import { Router } from "./router";
import type { Insight, Meta } from "./types";
import { ThemeProvider } from "./theme";
import { I18nProvider } from "./i18n";

beforeAll(() => {
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
});

/**
 * Display-layer integration coverage with the real Cytoscape instance
 * (no cytoscape mock): semantic zoom label tiers, hover focus, edge display
 * modes, weight-mapped edge styling, and supersedes visual preservation.
 */

function memory(id: string, importance: number): Insight {
  return {
    id,
    content: `Memory ${id}`,
    category: "fact",
    importance,
    tags: [],
    entities: [],
    source: "",
    access_count: 0,
    created_at: "",
    updated_at: "",
  };
}

const nodes = [
  memory("h", 5),
  memory("m", 3),
  memory("l", 1),
  memory("x", 1),
  memory("y", 1),
];
const edges = [
  { source_id: "h", target_id: "m", edge_type: "semantic", weight: 0.8, metadata: {}, created_at: "" },
  { source_id: "m", target_id: "l", edge_type: "semantic", weight: 0.2, metadata: {}, created_at: "" },
  { source_id: "m", target_id: "x", edge_type: "semantic", weight: 0.3, metadata: {}, created_at: "" },
  { source_id: "l", target_id: "h", edge_type: "supersedes", weight: 0.9, metadata: {}, created_at: "" },
  { source_id: "x", target_id: "y", edge_type: "semantic", weight: 0.4, metadata: {}, created_at: "" },
];

const meta: Meta = {
  version: "test",
  initial_store: "alpha",
  categories: ["fact"],
  edge_types: ["semantic", "supersedes"],
};

async function renderGraph(options?: {
  resizeObserver?: new (callback: ResizeObserverCallback) => ResizeObserver;
}) {
  window.localStorage.setItem("mnemon-language", "en");
  window.history.replaceState(null, "", "/s/alpha/graph");
  vi.stubGlobal(
    "ResizeObserver",
    options?.resizeObserver ??
      class {
        observe() {}
        disconnect() {}
      },
  );
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => ({
      ok: true,
      status: 200,
      json: async () => ({
        nodes,
        edges,
        total: nodes.length,
        truncated: false,
        updated_at: "",
      }),
    })),
  );
  const view = render(
    <I18nProvider>
      <ThemeProvider>
        <Profiler id="graph" onRender={() => (renderCounter.value += 1)}>
          <Router>
            <GraphView meta={meta} refresh={0} />
          </Router>
        </Profiler>
      </ThemeProvider>
    </I18nProvider>,
  );
  renderCounter.value = 0;
  await screen.findByLabelText("Label display mode");
  const container = document.querySelector(
    ".graph-canvas",
  ) as HTMLDivElement | null;
  expect(container).toBeTruthy();
  // The controls can render before the canvas mount effect initializes Cytoscape.
  await waitFor(() =>
    expect((container as unknown as { _cyreg?: { cy: Core } })._cyreg?.cy).toBeDefined(),
  );
  // jsdom has no layout: pin the renderer's cached container coords.
  const cy = (
    container as unknown as {
      _cyreg: { cy: Core };
    }
  )._cyreg.cy;
  (
    cy as unknown as { renderer: () => { containerBB: unknown[] } }
  )
    .renderer()
    .containerBB.splice(0, 5, 0, 0, 800, 600, 1);
  return { container, cy, unmount: view.unmount };
}

const renderCounter = { value: 0 };
const edgeBetween = (cy: Core, a: string, b: string, type = "semantic") =>
  cy.edges(
    `[source = "${a}"][target = "${b}"][label = "${type}"]`,
  ).first() as unknown as EdgeSingular;

it("applies semantic zoom label tiers and weight-mapped edge styling", async () => {
  const { cy } = await renderGraph();
  // Settle framing (review fix): the initial auto-fit runs once the layout
  // converges — not before (jsdom has no real container size, so the spy
  // proves the timing; viewport framing is verified in a real browser).
  const fitSpy = vi.spyOn(cy, "fit");
  await waitFor(() => expect(fitSpy).toHaveBeenCalled(), { timeout: 12000 });
  // Tiers are asserted at explicit zoom levels (independent of framing).
  const setZoom = async (level: number) => {
    act(() => {
      cy.zoom({ level, renderedPosition: { x: 400, y: 300 } });
    });
    await new Promise((resolve) => setTimeout(resolve, 250));
  };
  await setZoom(1);
  expect(cy.getElementById("h").hasClass("labeled")).toBe(true);
  expect(cy.getElementById("m").hasClass("labeled")).toBe(true);
  expect(cy.getElementById("l").hasClass("labeled")).toBe(false);
  expect(cy.getElementById("x").hasClass("labeled")).toBe(false);
  // Weight tiers drive width/opacity data (0.2 -> 0.8px, 0.8 -> 2.2px).
  expect(parseFloat(String(edgeBetween(cy, "m", "l").style("width")))).toBe(0.8);
  expect(parseFloat(String(edgeBetween(cy, "h", "m").style("width")))).toBe(2.2);
  expect(Number(edgeBetween(cy, "m", "l").style("opacity"))).toBeLessThan(
    Number(edgeBetween(cy, "h", "m").style("opacity")),
  );
  // The supersedes edge keeps its gold dashed arrow semantics (acceptance 13).
  const supersedes = edgeBetween(cy, "l", "h", "supersedes");
  expect(supersedes.style("line-style")).toBe("dashed");
  expect(supersedes.style("line-color")).toBe("rgb(193,152,87)");
  expect(supersedes.style("target-arrow-shape")).toBe("triangle");
  expect(supersedes.hasClass("labeled")).toBe(true);
  expect(Number(supersedes.style("text-opacity"))).toBe(1);
  const detailArrowScale = Number(supersedes.style("arrow-scale"));

  // Zoom out far: only the most important labels remain, weak edges dim.
  await setZoom(0.4);
  expect(cy.getElementById("m").hasClass("labeled")).toBe(false);
  expect(cy.getElementById("h").hasClass("labeled")).toBe(true);
  expect(edgeBetween(cy, "m", "l").hasClass("zoom-dim")).toBe(true);
  expect(edgeBetween(cy, "h", "m").hasClass("zoom-dim")).toBe(false);
  expect(edgeBetween(cy, "l", "h", "supersedes").hasClass("zoom-dim")).toBe(
    false,
  );
  expect(supersedes.hasClass("labeled")).toBe(false);
  expect(Number(supersedes.style("text-opacity"))).toBe(0);
  expect(Number(supersedes.style("arrow-scale"))).toBeGreaterThan(
    detailArrowScale,
  );
  expect(supersedes.style("line-style")).toBe("dashed");
  expect(supersedes.style("target-arrow-shape")).toBe("triangle");

  // Explicit modes override the zoom tiers.
  const labelMode = screen.getByLabelText("Label display mode");
  await act(async () => {
    fireEvent.change(labelMode, { target: { value: "all" } });
  });
  expect(cy.getElementById("x").hasClass("labeled")).toBe(true);
  expect(supersedes.hasClass("labeled")).toBe(true);
  await act(async () => {
    fireEvent.change(labelMode, { target: { value: "hidden" } });
  });
  expect(cy.getElementById("h").hasClass("labeled")).toBe(false);
  expect(supersedes.hasClass("labeled")).toBe(false);
  expect(
    (document.querySelector(".graph-canvas") as unknown as {
      _cyreg: { cy: Core };
    })._cyreg.cy,
  ).toBe(cy);
}, 20000);

it("keeps fading labels and halos in their theme colors when switching nodes", async () => {
  const { cy } = await renderGraph();
  act(() => cy.zoom(0.4));
  const previous = cy.getElementById("x");
  await act(async () => {
    previous.emit("tap");
  });
  await waitFor(() => expect(Number(previous.style("text-opacity"))).toBe(1));
  const fontSize = previous.style("font-size");
  const outline = previous.style("text-outline-color");
  const halo = previous.style("underlay-color");
  await act(async () => {
    cy.getElementById("h").emit("tap");
  });
  expect(previous.style("font-size")).toBe(fontSize);
  expect(previous.style("text-outline-color")).toBe(outline);
  expect(previous.style("underlay-color")).toBe(halo);
  await waitFor(() => expect(Number(previous.style("text-opacity"))).toBe(0));
});

it("inspects a connection without stealing node selection and dismisses its details", async () => {
  const { cy } = await renderGraph();
  const positions = {
    h: { x: 100, y: 100 },
    m: { x: 200, y: 300 },
    l: { x: 100, y: 500 },
    x: { x: 600, y: 300 },
    y: { x: 700, y: 500 },
  };
  for (const [id, position] of Object.entries(positions))
    cy.getElementById(id).position(position).lock();
  act(() => {
    cy.zoom(1);
    cy.pan({ x: 0, y: 0 });
  });
  await act(async () => {
    cy.getElementById("h").emit("tap");
  });
  const edge = edgeBetween(cy, "m", "x");
  await waitFor(() => expect(edge.midpoint().y).toBeCloseTo(300, 5));
  const point = edge.midpoint();
  expect(point.x).toBeGreaterThan(300);
  expect(point.x).toBeLessThan(500);
  (cy as unknown as { renderer: () => { containerBB: unknown[] } })
    .renderer().containerBB.splice(0, 5, 0, 0, 800, 600, 1);
  const canvas = document.querySelector(".graph-canvas canvas")!;
  await act(async () => {
    for (const type of ["mousedown", "mouseup"]) {
      const event = new MouseEvent(type, {
        bubbles: true, cancelable: true, clientX: point.x, clientY: point.y, button: 0,
      });
      Object.defineProperty(event, "which", { value: 1 });
      (type === "mousedown" ? canvas : window).dispatchEvent(event);
    }
  });
  const details = screen.getByRole("status", { name: "Relationship details" });
  expect(details.closest(".graph-frame")).not.toBeNull();
  expect(details.closest(".graph-surface")).toBeNull();
  expect(details).toHaveTextContent("Memory m");
  expect(details).toHaveTextContent("Memory x");
  expect(details).toHaveTextContent("Semantic");
  expect(details).toHaveTextContent("Weight 0.3");
  expect(edge.hasClass("inspected")).toBe(true);
  expect(edge.selectable()).toBe(false);
  expect(cy.getElementById("h").selected()).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Close relationship details" }));
  expect(screen.queryByRole("status", { name: "Relationship details" })).toBeNull();
  expect(edge.hasClass("inspected")).toBe(false);
  const correction = edgeBetween(cy, "l", "h", "supersedes");
  await act(async () => {
    correction.emit("tap");
  });
  expect(screen.getByRole("status", { name: "Relationship details" })).toHaveTextContent("Supersedes");
  expect(correction.style("line-color")).toBe("rgb(193,152,87)");
  await act(async () => {
    cy.emit("tap");
  });
  expect(screen.queryByRole("status", { name: "Relationship details" })).toBeNull();
  await act(async () => {
    edge.emit("tap");
  });
  fireEvent.click(screen.getByRole("button", { name: /Memory h/ }));
  await waitFor(() => expect(screen.queryByRole("status", { name: "Relationship details" })).toBeNull());
});

it("keeps pressed connections subtle without disabling their interaction or semantics", async () => {
  const { cy } = await renderGraph();
  for (const edge of [
    edgeBetween(cy, "h", "m"),
    edgeBetween(cy, "l", "h", "supersedes"),
  ]) {
    // Runtime activation helpers are absent from the older @types package.
    const press = edge as EdgeSingular & {
      activate(): void;
      unactivate(): void;
    };
    press.activate();
    expect(edge.active()).toBe(true);
    expect(edge.style("events")).toBe("yes");
    expect(edge.selectable()).toBe(false);
    expect(parseFloat(String(edge.style("overlay-padding")))).toBe(1);
    expect(Number(edge.style("overlay-opacity"))).toBe(0.12);
    expect(edge.style("overlay-color")).toBe("rgb(159,194,240)");
    if (edge.data("supersedes") === "yes") {
      expect(edge.style("line-style")).toBe("dashed");
      expect(edge.style("line-color")).toBe("rgb(193,152,87)");
      expect(edge.style("target-arrow-color")).toBe("rgb(193,152,87)");
    }
    press.unactivate();
    expect(Number(edge.style("overlay-opacity"))).toBe(0);
  }
});

it("locates list selections at a readable scale and recenters repeated selections", async () => {
  const { cy, container } = await renderGraph();
  container!.style.padding = "0px";
  const scroll = vi.spyOn(container!, "scrollIntoView");
  Object.defineProperties(container, {
    clientWidth: { value: 800 },
    clientHeight: { value: 600 },
  });
  cy.resize();
  // Hold the target still while the initial force layout settles around it.
  const target = cy.getElementById("h");
  target.position({ x: 900, y: 700 }).lock();
  act(() => {
    cy.zoom(0.2);
    cy.pan({ x: 0, y: 0 });
  });
  const select = () =>
    fireEvent.click(screen.getByRole("button", { name: /Memory h/ }));
  const expectCentered = () => {
    expect(target.renderedPosition().x).toBeCloseTo(cy.width() / 2, 0);
    expect(target.renderedPosition().y).toBeCloseTo(cy.height() / 2, 0);
  };
  await act(async () => select());
  await waitFor(() => {
    expect(cy.zoom()).toBeGreaterThanOrEqual(1.4);
    expectCentered();
  });
  expect(scroll).toHaveBeenCalledWith({ behavior: "smooth", block: "center" });
  // Reclicking the same URL selection must recenter, preserving a closer zoom.
  act(() => {
    cy.zoom(2);
    cy.pan({ x: 0, y: 0 });
  });
  await act(async () => select());
  await waitFor(expectCentered);
  expect(cy.zoom()).toBeCloseTo(2, 6);
  expect(target.position()).toEqual({ x: 900, y: 700 });
  expect((container as unknown as { _cyreg: { cy: Core } })._cyreg.cy).toBe(cy);
});

it("gives selected edges higher contrast without changing special relation semantics", async () => {
  const { cy } = await renderGraph();
  const weak = edgeBetween(cy, "m", "l");
  const supersedes = edgeBetween(cy, "l", "h", "supersedes");
  act(() => cy.zoom(0.4));
  await waitFor(() => expect(weak.hasClass("zoom-dim")).toBe(true));
  const baseOpacity = Number(weak.style("opacity"));
  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: /Memory l/ }));
  });
  await waitFor(() =>
    expect(Number(weak.style("opacity"))).toBeGreaterThan(baseOpacity),
  );
  expect(weak.hasClass("selected-link")).toBe(true);
  expect(supersedes.hasClass("selected-link")).toBe(true);
  expect(edgeBetween(cy, "x", "y").hasClass("selected-link")).toBe(false);
  expect(supersedes.style("line-color")).toBe("rgb(193,152,87)");
  expect(supersedes.style("line-style")).toBe("dashed");
  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: /Memory x/ }));
  });
  expect(weak.hasClass("selected-link")).toBe(false);
  expect(edgeBetween(cy, "x", "y").hasClass("selected-link")).toBe(true);
  await act(async () => {
    fireEvent.click(
      screen.getByRole("button", { name: "Close selected memory" }),
    );
  });
  expect(cy.edges(".selected-link")).toHaveLength(0);
});

it.each(["Back", "Back/Forward", "close"])("resets two-hop focus after %s", async (exit) => {
  const { cy } = await renderGraph();
  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: /Memory h/ }));
    fireEvent.change(screen.getByLabelText("View mode"), {
      target: { value: "focus2" },
    });
  });
  expect(cy.getElementById("x").hasClass("focus-set")).toBe(true);
  await act(async () => {
    if (exit.startsWith("Back")) window.history.back();
    else
      fireEvent.click(
        screen.getByRole("button", { name: "Close selected memory" }),
      );
  });
  await waitFor(() => {
    expect(
      screen.queryByRole("button", { name: "Close selected memory" }),
    ).toBeNull();
    expect(screen.getByLabelText("View mode")).toHaveValue("global");
  });
  await act(async () => {
    if (exit === "Back/Forward") window.history.forward();
    else fireEvent.click(screen.getByRole("button", { name: /Memory h/ }));
  });
  await screen.findByRole("button", { name: "Close selected memory" });
  expect(screen.getByLabelText("View mode")).toHaveValue("global");
  expect(cy.getElementById("x").hasClass("focus-set")).toBe(false);
  expect(cy.getElementById("x").hasClass("faded")).toBe(true);
  // A subsequent list selection must also stay in the global mode.
  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: /Memory m/ }));
  });
  expect(screen.getByLabelText("View mode")).toHaveValue("global");
  expect(cy.getElementById("y").hasClass("focus-set")).toBe(false);
  expect(
    (document.querySelector(".graph-canvas") as unknown as {
      _cyreg: { cy: Core };
    })._cyreg.cy,
  ).toBe(cy);
});

it("dims unrelated elements on hover and filters edges by display mode", async () => {
  const { cy } = await renderGraph();
  // The render loop re-derives container coords (zeros in jsdom) whenever a
  // frame runs, so re-pin right before pointer event synthesis.
  const pin = () =>
    (
      cy as unknown as { renderer: () => { containerBB: unknown[] } }
    )
      .renderer()
      .containerBB.splice(0, 5, 0, 0, 800, 600, 1);
  // Let the force layout separate the nodes so hover hit-testing is
  // unambiguous, then bring the graph into the visible canvas area.
  await waitFor(
    () => {
      const points = ["h", "m", "l", "x"].map((id) =>
        cy.getElementById(id).position(),
      );
      let closest = Number.POSITIVE_INFINITY;
      for (let i = 0; i < points.length; i++)
        for (let j = i + 1; j < points.length; j++)
          closest = Math.min(
            closest,
            Math.hypot(points[i].x - points[j].x, points[i].y - points[j].y),
          );
      expect(closest).toBeGreaterThan(30);
    },
    { timeout: 10000 },
  );
  act(() => cy.pan({ x: 400, y: 300 }));
  const h = cy.getElementById("h");
  const canvas = document.querySelector(".graph-canvas canvas") as HTMLCanvasElement;
  // Cytoscape derives node hover events from mousemove; without a pressed
  // button the event target must sit inside the graph container.
  const move = (clientX: number, clientY: number) => {
    const event = new MouseEvent("mousemove", {
      bubbles: true,
      cancelable: true,
      clientX,
      clientY,
    });
    Object.defineProperty(event, "which", { value: 1 });
    canvas.dispatchEvent(event);
  };
  pin();
  const position = h.renderedPosition();
  // Element-local hover (§6): the node gets a gentle stroke and its direct
  // edges lift — never a whole-graph fade, React re-render or stylesheet
  // reapplication while sweeping the cursor. (Render count deltas isolate the
  // hover dispatch from background commits; the style probe only counts
  // stylesheet *applications*, since Cytoscape reads cy.style() internally.)
  const originalStyle = cy.style.bind(cy) as (...args: unknown[]) => unknown;
  const styleApplications = { count: 0 };
  const styleSpy = vi.spyOn(cy, "style").mockImplementation(
    ((...args: unknown[]) => {
      if (args.length > 0 && typeof args[0] !== "string")
        styleApplications.count += 1;
      return originalStyle(...args);
    }) as unknown as typeof cy.style,
  );
  const rendersBeforeHover = renderCounter.value;
  move(position.x, position.y);
  expect(h.hasClass("hovered")).toBe(true);
  expect(edgeBetween(cy, "h", "m").hasClass("hover-link")).toBe(true);
  expect(cy.elements(".faded").length).toBe(0);
  expect(cy.getElementById("m").hasClass("focus-set")).toBe(false);
  expect(cy.getElementById("m").hasClass("highlight")).toBe(false);
  expect(renderCounter.value).toBe(rendersBeforeHover);
  expect(styleApplications.count).toBe(0);
  pin();
  move(30, 550); // empty corner: hover clears, still element-local
  expect(h.hasClass("hovered")).toBe(false);
  expect(edgeBetween(cy, "h", "m").hasClass("hover-link")).toBe(false);
  expect(cy.elements(".faded").length).toBe(0);
  expect(renderCounter.value).toBe(rendersBeforeHover);
  expect(styleApplications.count).toBe(0);
  styleSpy.mockRestore();

  // Edge mode "strong": weak edges are hidden from view (display-only).
  await act(async () => {
    fireEvent.change(screen.getByLabelText("Edge display mode"), {
      target: { value: "strong" },
    });
  });
  expect(edgeBetween(cy, "m", "l").hasClass("mode-hidden")).toBe(true);
  expect(edgeBetween(cy, "m", "x").hasClass("mode-hidden")).toBe(true);
  expect(edgeBetween(cy, "h", "m").hasClass("mode-hidden")).toBe(false);
  expect(
    edgeBetween(cy, "l", "h", "supersedes").hasClass("mode-hidden"),
  ).toBe(false);

  // Edge mode "selected": pick h from the accessible list, then only edges
  // touching h remain visible.
  await act(async () => {
    fireEvent.change(screen.getByLabelText("Edge display mode"), {
      target: { value: "all" },
    });
    fireEvent.click(screen.getByRole("button", { name: /Memory h/ }));
  });
  await act(async () => {
    fireEvent.change(screen.getByLabelText("Edge display mode"), {
      target: { value: "selected" },
    });
  });
  expect(edgeBetween(cy, "m", "x").hasClass("mode-hidden")).toBe(true);
  expect(edgeBetween(cy, "m", "l").hasClass("mode-hidden")).toBe(true);
  expect(edgeBetween(cy, "h", "m").hasClass("mode-hidden")).toBe(false);
  expect(
    edgeBetween(cy, "l", "h", "supersedes").hasClass("mode-hidden"),
  ).toBe(false);
});

it("supports selection emphasis and global/focus dual modes", async () => {
  const liveCy = () =>
    (
      document.querySelector(".graph-canvas") as unknown as {
        _cyreg: { cy: Core };
      }
    )._cyreg.cy;
  const { cy } = await renderGraph();
  const view = screen.getByLabelText("View mode");

  // Clicking a list entry selects h: one-hop neighborhood highlighted,
  // unrelated nodes faded, detail panel opens (acceptance 10).
  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: /Memory h/ }));
  });
  expect(
    screen.getByRole("heading", { name: "Memory h" }),
  ).toBeVisible();
  expect(cy.getElementById("h").hasClass("focus-set")).toBe(true);
  expect(cy.getElementById("m").hasClass("focus-set")).toBe(true);
  expect(cy.getElementById("x").hasClass("faded")).toBe(true);
  expect(cy.getElementById("y").hasClass("faded")).toBe(true);

  // Two-hop focus is display-layer only: no refetch, same instance, third-hop
  // node stays faded while the two-hop set keeps its labels (acceptance 11).
  const fetchCalls = () =>
    (globalThis.fetch as unknown as { mock?: { calls: unknown[] } }).mock
      ?.calls.length ?? 0;
  const before = fetchCalls();
  await act(async () => {
    fireEvent.change(view, { target: { value: "focus2" } });
  });
  expect(fetchCalls()).toBe(before);
  expect(cy.getElementById("x").hasClass("focus-set")).toBe(true);
  expect(cy.getElementById("y").hasClass("faded")).toBe(true);
  expect(cy.getElementById("l").hasClass("labeled")).toBe(true);

  // One-hop focus reuses the focus URL parameter and the API neighborhood.
  await act(async () => {
    fireEvent.change(view, { target: { value: "focus" } });
  });
  expect(new URLSearchParams(window.location.search).get("focus")).toBe("h");
  const calls = (
    globalThis.fetch as unknown as { mock: { calls: unknown[][] } }
  ).mock.calls;
  expect(
    calls.some(([url]) => String(url).includes("focus=h")),
  ).toBe(true);
  // The remounted instance applies focus-mode labels (auto tier overridden).
  let focused: Core | null = null;
  await waitFor(() => {
    focused = liveCy();
    expect(focused).not.toBe(cy);
    expect(focused!.getElementById("l").hasClass("labeled")).toBe(true);
  });

  // Back to global: focus (and with it the fallback selection) is cleared and
  // the remounted view has no emphasis left.
  await act(async () => {
    fireEvent.change(screen.getByLabelText("View mode"), {
      target: { value: "global" },
    });
  });
  expect(new URLSearchParams(window.location.search).get("focus")).toBeNull();
  let global2: Core | null = null;
  await waitFor(() => {
    global2 = liveCy();
    expect(global2).not.toBe(focused);
    expect(global2!.getElementById("y").hasClass("faded")).toBe(false);
    expect(global2!.getElementById("h").hasClass("focus-set")).toBe(false);
  });
});

it("wires component drag into physics, resizes with the container, and unmounts clean", async () => {
  const observers: ResizeObserverCallback[] = [];
  const { cy, unmount } = await renderGraph({
    resizeObserver: class {
      constructor(callback: ResizeObserverCallback) {
        observers.push(callback);
      }
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  });
  expect(observers.length).toBeGreaterThanOrEqual(1);
  // Container resizes reach the instance.
  const resizeSpy = vi.spyOn(cy, "resize");
  observers.forEach((callback) => callback([], {} as ResizeObserver));
  expect(resizeSpy).toHaveBeenCalled();

  // Dragging through the component wires the physics: the grabbed node
  // follows the cursor and the forces move its neighbors (§1 wiring).
  const pin = () =>
    (
      cy as unknown as { renderer: () => { containerBB: unknown[] } }
    )
      .renderer()
      .containerBB.splice(0, 5, 0, 0, 800, 600, 1);
  // Let the layout separate the nodes so the drag hits the intended node.
  await waitFor(
    () => {
      const points = ["h", "m", "l", "x", "y"].map((id) =>
        cy.getElementById(id).position(),
      );
      let closest = Number.POSITIVE_INFINITY;
      for (let i = 0; i < points.length; i++)
        for (let j = i + 1; j < points.length; j++)
          closest = Math.min(
            closest,
            Math.hypot(points[i].x - points[j].x, points[i].y - points[j].y),
          );
      expect(closest).toBeGreaterThan(30);
    },
    { timeout: 10000 },
  );
  pin();
  act(() => cy.pan({ x: 400, y: 300 }));
  const h = cy.getElementById("h");
  const canvas = document.querySelector(
    ".graph-canvas canvas",
  ) as HTMLCanvasElement;
  const grab = h.renderedPosition();
  const othersBefore = cy
    .nodes()
    .filter((n) => n.id() !== "h")
    .map((n) => ({ ...(n as NodeSingular).position() }));
  const mouse = (type: "mousedown" | "mousemove" | "mouseup", x: number, y: number) => {
    const target = type === "mousedown" ? canvas : window;
    const event = new MouseEvent(type, {
      bubbles: true,
      cancelable: true,
      clientX: x,
      clientY: y,
      button: 0,
      buttons: 1,
    });
    Object.defineProperty(event, "which", { value: 1 });
    target.dispatchEvent(event);
  };
  mouse("mousedown", grab.x, grab.y);
  mouse("mousemove", grab.x + 60, grab.y);
  await new Promise((resolve) => setTimeout(resolve, 350));
  expect(h.grabbed()).toBe(true);
  expect(h.position().x).toBeCloseTo(grab.x - 400 + 60, 0);
  const othersAfter = cy
    .nodes()
    .filter((n) => n.id() !== "h")
    .map((n) => ({ ...(n as NodeSingular).position() }));
  expect(othersAfter).not.toEqual(othersBefore);
  mouse("mouseup", grab.x + 60, grab.y);
  expect(h.grabbed()).toBe(false);

  // Unmount tears the instance, simulation and listeners down without errors.
  unmount();
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 200));
  });
  expect(document.querySelector(".graph-canvas")).toBeNull();
  // A stray wheel event after unmount must not throw.
  document.body.dispatchEvent(
    new WheelEvent("wheel", { bubbles: true, cancelable: true, deltaY: -100 }),
  );
});

it("keeps a released node and viewport across selection, theme, and language changes", async () => {
  const { cy, container } = await renderGraph();
  // Wait until the nodes separate so the drag hits the intended node.
  await waitFor(
    () => {
      const points = ["h", "m", "l", "x", "y"].map((id) =>
        cy.getElementById(id).position(),
      );
      let closest = Number.POSITIVE_INFINITY;
      for (let i = 0; i < points.length; i++)
        for (let j = i + 1; j < points.length; j++)
          closest = Math.min(
            closest,
            Math.hypot(points[i].x - points[j].x, points[i].y - points[j].y),
          );
      expect(closest).toBeGreaterThan(30);
    },
    { timeout: 10000 },
  );
  const pin = () =>
    (
      cy as unknown as { renderer: () => { containerBB: unknown[] } }
    )
      .renderer()
      .containerBB.splice(0, 5, 0, 0, 800, 600, 1);
  pin();
  act(() => cy.pan({ x: 400, y: 300 }));
  const h = cy.getElementById("h");
  const canvas = document.querySelector(
    ".graph-canvas canvas",
  ) as HTMLCanvasElement;
  const mouse = (type: "mousedown" | "mousemove" | "mouseup", x: number, y: number) => {
    const target = type === "mousedown" ? canvas : window;
    const event = new MouseEvent(type, {
      bubbles: true,
      cancelable: true,
      clientX: x,
      clientY: y,
      button: 0,
      buttons: 1,
    });
    Object.defineProperty(event, "which", { value: 1 });
    target.dispatchEvent(event);
  };

  // Drag h far outside its cluster and release it there (user placement).
  const grab = h.renderedPosition();
  mouse("mousedown", grab.x, grab.y);
  mouse("mousemove", grab.x + 150, grab.y - 100);
  const drop = { ...h.position() };
  mouse("mouseup", grab.x + 150, grab.y - 100);
  await new Promise((resolve) => setTimeout(resolve, 1500)); // rebound + cooldown
  expect(h.position().x).toBeCloseTo(drop.x, 6);
  expect(h.position().y).toBeCloseTo(drop.y, 6);

  // Selection change (URL state -> emphasis effects) must not move it.
  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: /Memory m/ }));
  });
  await new Promise((resolve) => setTimeout(resolve, 500));
  expect(h.position().x).toBeCloseTo(drop.x, 6);

  // Theme switches re-apply the whole stylesheet: position must survive.
  const setTheme = async (mode: string) => {
    await act(async () => {
      localStorage.setItem("mnemon-appearance", mode);
      window.dispatchEvent(
        new StorageEvent("storage", {
          key: "mnemon-appearance",
          newValue: mode,
          storageArea: localStorage,
        }),
      );
    });
    await new Promise((resolve) => setTimeout(resolve, 500));
  };
  await setTheme("dark");
  expect(document.documentElement.dataset.theme).toBe("dark");
  expect(h.position().x).toBeCloseTo(drop.x, 6);
  await setTheme("light");
  expect(document.documentElement.dataset.theme).toBe("light");
  expect(h.position().x).toBeCloseTo(drop.x, 6);
  expect(h.position().y).toBeCloseTo(drop.y, 6);

  fireEvent.change(screen.getByLabelText("Label display mode"), {
    target: { value: "all" },
  });
  await act(async () => edgeBetween(cy, "h", "m").emit("tap"));
  expect(screen.getByLabelText("Relationship details")).toHaveTextContent("Weight 0.8");
  const viewport = { zoom: cy.zoom(), pan: { ...cy.pan() } };
  await act(async () => {
    localStorage.setItem("mnemon-language", "zh");
    window.dispatchEvent(
      new StorageEvent("storage", {
        key: "mnemon-language",
        newValue: "zh",
        storageArea: localStorage,
      }),
    );
  });
  expect(screen.getByLabelText("视图模式")).toHaveTextContent("全局网络");
  expect(screen.getByLabelText("标签显示模式")).toHaveValue("all");
  expect(screen.getByLabelText("连线显示模式")).toHaveTextContent("强关联连线");
  expect(screen.getByLabelText("关系详情")).toHaveTextContent("权重 0.8");
  expect(screen.getByRole("button", { name: "关闭关系详情" })).toBeVisible();
  expect((container as unknown as { _cyreg: { cy: Core } })._cyreg.cy).toBe(cy);
  expect(cy.zoom()).toBe(viewport.zoom);
  expect(cy.pan()).toEqual(viewport.pan);
  expect(h.position().x).toBeCloseTo(drop.x, 6);
  expect(h.position().y).toBeCloseTo(drop.y, 6);
}, 30000);
