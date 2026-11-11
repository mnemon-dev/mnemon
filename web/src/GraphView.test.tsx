import { act, fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { GraphView, graphStyle } from "./GraphView";
import { Router } from "./router";
import type { Insight, Meta } from "./types";
import { I18nProvider } from "./i18n";
import { ThemeProvider } from "./theme";

vi.mock("cytoscape", () => {
  // Minimal chainable collection stub for the visual-mode class handling.
  const collection = () => {
    const c = {
      *[Symbol.iterator]() {},
      addClass: vi.fn(() => c),
      removeClass: vi.fn(() => c),
      toggleClass: vi.fn(() => c),
      filter: vi.fn(() => c),
      not: vi.fn(() => c),
      union: vi.fn(() => c),
      nonempty: vi.fn(() => false),
      isNode: vi.fn(() => false),
      connectedEdges: vi.fn(() => c),
      closedNeighborhood: vi.fn(() => c),
      unselect: vi.fn(),
      select: vi.fn(),
      position: vi.fn(),
      remove: vi.fn(),
      id: vi.fn(() => ""),
    };
    return c;
  };
  return {
    default: () => ({
      on: vi.fn(),
      style: () => {
        const sheet = { selector: vi.fn(), style: vi.fn(), update: vi.fn() };
        sheet.selector.mockReturnValue(sheet);
        sheet.style.mockReturnValue(sheet);
        return sheet;
      },
      resize: vi.fn(),
      stop: vi.fn(),
      animate: vi.fn(),
      destroy: vi.fn(),
      nodes: collection,
      edges: collection,
      elements: collection,
      getElementById: collection,
      fit: vi.fn(),
      zoom: vi.fn(() => 1),
      width: () => 500,
      height: () => 500,
      batch: (run: () => void) => run(),
      add: vi.fn((def: { data: Record<string, unknown> }) => {
        const data = { ...def.data };
        return {
          id: () => data.id,
          data: (keyOrPatch: unknown, value?: unknown) => {
            if (typeof keyOrPatch === "string") {
              if (value === undefined) return data[keyOrPatch];
              data[keyOrPatch] = value;
              return undefined;
            }
            if (keyOrPatch && typeof keyOrPatch === "object")
              Object.assign(data, keyOrPatch);
            return data;
          },
          position: vi.fn(() => ({ x: 0, y: 0 })),
          remove: vi.fn(),
        };
      }),
    }),
  };
});
it("preserves correction edge semantics when replacing the theme stylesheet", () => {
  for (const palette of [
    {
      label: "#505058",
      edge: "#a8a8b2",
      selected: "#26262b",
      outline: "#f7f7f8",
      highlight: "#9fc2f0",
    },
    {
      label: "#cdcdd3",
      edge: "#65656f",
      selected: "#d4d4d8",
      outline: "#1a1a1a",
      highlight: "#5478c0",
    },
  ]) {
    const styles = graphStyle(palette);
    const styleOf = (selector: string) => {
      const rule = styles.find((item) => item.selector === selector);
      return rule && "style" in rule ? rule.style : undefined;
    };
    const correction = styles.find(
      (rule) => rule.selector === 'edge[supersedes = "yes"]',
    );
    expect(
      correction && "style" in correction ? correction.style : undefined,
    ).toMatchObject({
      "line-color": "#c19857",
      "target-arrow-color": "#c19857",
      "line-style": "dashed",
      color: palette.label,
      "text-background-color": palette.outline,
    });
    expect(styles).toHaveLength(18);
    expect(styleOf("core")).toMatchObject({
      "active-bg-color": palette.highlight,
      "active-bg-size": 6,
      "active-bg-opacity": 0.12,
    });
    expect(styleOf("edge:active")).toMatchObject({
      "overlay-color": palette.highlight,
      "overlay-padding": 1,
      "overlay-opacity": 0.12,
    });
    // Special relation rules stay after generic edge styling, including the
    // selection contrast rule, so their gold dashed direction is preserved.
    expect(styles.slice(-3).map((rule) => rule.selector)).toEqual([
      'edge[supersedes = "yes"]',
      'edge[supersedes = "yes"].zoom-overview',
      'edge[supersedes = "yes"].labeled',
    ]);
    expect(styleOf('edge[supersedes = "yes"]')).toMatchObject({
      "arrow-scale": 1.2,
      "text-opacity": 0,
    });
    expect(styleOf('edge[supersedes = "yes"].zoom-overview')).toMatchObject({
      "arrow-scale": 1.8,
    });
    expect(styleOf('edge[supersedes = "yes"].labeled')).toMatchObject({
      "text-opacity": 1,
    });
    // Semantic zoom keeps labels hidden by default; classes reveal them.
    expect(styleOf("node")).toMatchObject({
      "text-opacity": 0,
      "text-outline-color": palette.outline,
      "text-outline-width": 0,
      "underlay-color": palette.highlight,
      "underlay-opacity": 0,
      "underlay-padding": 5,
    });
    expect(
      styleOf(
        "node.labeled, node.hovered, node:selected, node.focus-set",
      ),
    ).toMatchObject({
      "text-opacity": 1,
    });
    // Edge weight mapping drives width/opacity through element data.
    expect(styleOf("edge")).toMatchObject({
      width: "data(width)",
      opacity: "data(opacity)",
    });
    // Hover feedback: soft underlay uses the theme-aware highlight colour.
    expect(styleOf("node.highlight")).toMatchObject({
      "underlay-color": palette.highlight,
    });
    // Element-local hover: a gentle accent stroke on the node and a mild
    // lift on its direct edges — nothing else changes (§6).
    expect(styleOf("node.hovered")).toMatchObject({
      "border-color": palette.highlight,
    });
    expect(styleOf("edge.hover-link")).toMatchObject({ opacity: 0.8 });
    // §6 supplementary targets: ~200ms easing including the halo and the
    // selection stroke, and fade floors that keep dimmed elements readable
    // on light and dark canvases alike.
    expect(styleOf("node")).toMatchObject({
      "transition-duration": 200,
      "transition-property":
        "text-opacity, opacity, underlay-opacity, underlay-padding, border-color, border-width, text-outline-width, color",
    });
    expect(styleOf("edge")).toMatchObject({ "transition-duration": 200 });
    expect(styleOf("node.faded")).toMatchObject({ opacity: 0.35 });
    expect(styleOf("edge.faded")).toMatchObject({ opacity: 0.3 });
    expect(styleOf("edge.zoom-dim")).toMatchObject({ opacity: 0.25 });
    expect(styleOf("edge.selected-link")).toMatchObject({ opacity: 0.85 });
    // Selection uses the theme-adapted accent (never near-black/near-white)
    // with a softened label knock-out.
    expect(styleOf("node:selected")).toMatchObject({
      "border-color": palette.highlight,
      "text-outline-width": 2,
      color: palette.label,
    });
  }
});
const memory: Insight = {
  id: "m1",
  content: "A connected thought",
  category: "fact",
  importance: 4,
  tags: [],
  entities: ["Project"],
  source: "literal-source",
  access_count: 0,
  created_at: "",
  updated_at: "",
};
const meta: Meta = {
  version: "test",
  initial_store: "alpha",
  categories: ["fact"],
  edge_types: ["semantic", "supersedes"],
};

it("supports keyboard-equivalent graph selection, reader links, URL focus, and Back restoration", async () => {
  vi.stubGlobal(
    "ResizeObserver",
    class {
      observe() {}
      disconnect() {}
    },
  );
  window.history.replaceState(null, "", "/s/alpha/graph");
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => ({
      ok: true,
      status: 200,
      json: async () => ({
        nodes: [memory],
        edges: [
          {
            source_id: "m1",
            target_id: "m1",
            edge_type: "supersedes",
            weight: 0.8,
            metadata: { reason: "correction" },
          },
        ],
        total: 1,
        truncated: false,
        updated_at: "",
      }),
    })),
  );
  render(
    <I18nProvider>
      <ThemeProvider>
        <Router>
          <GraphView meta={meta} refresh={0} />
        </Router>
      </ThemeProvider>
    </I18nProvider>,
  );
  const node = await screen.findByRole("button", {
    name: /A connected thought/,
  });
  fireEvent.click(node);
  expect(node).toHaveAttribute("aria-pressed", "true");
  expect(new URLSearchParams(window.location.search).get("selected")).toBe(
    "m1",
  );
  expect(screen.getByText("literal-source")).toBeVisible();
  expect(
    screen.getByRole("link", { name: "Read full memory" }),
  ).toHaveAttribute("href", "/s/alpha/memories/m1?selected=m1");
  fireEvent.click(screen.getByRole("button", { name: "Expand neighborhood" }));
  expect(new URLSearchParams(window.location.search).get("focus")).toBe("m1");
  await screen.findByRole("button", { name: "Clear neighborhood focus" });
  expect(
    screen.getByText(/Weight 0.8 · reason: correction/),
  ).toBeInTheDocument();
  for (const label of [
    "Zoom in",
    "Zoom out",
    "Fit graph",
    "Reset graph layout",
  ])
    expect(screen.getByRole("button", { name: label })).toBeVisible();
  for (const label of [
    "View mode",
    "Label display mode",
    "Edge display mode",
  ])
    expect(screen.getByLabelText(label)).toBeVisible();
  fireEvent.click(
    screen.getByRole("button", { name: "Close selected memory" }),
  );
  expect(screen.queryByRole("link", { name: "Read full memory" })).toBeNull();
  await act(async () => {
    window.history.back();
  });
  await screen.findByRole("link", { name: "Read full memory" });
});
