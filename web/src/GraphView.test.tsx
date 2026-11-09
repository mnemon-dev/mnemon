import { act, fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { GraphView, graphStyle } from "./GraphView";
import { Router } from "./router";
import type { Insight, Meta } from "./types";
import { I18nProvider } from "./i18n";
import { ThemeProvider } from "./theme";

vi.mock("cytoscape", () => ({
  default: () => ({
    on: vi.fn(),
    style: () => {
      const sheet = { selector: vi.fn(), style: vi.fn(), update: vi.fn() };
      sheet.selector.mockReturnValue(sheet);
      sheet.style.mockReturnValue(sheet);
      return sheet;
    },
    resize: vi.fn(),
    destroy: vi.fn(),
    nodes: () => ({ unselect: vi.fn() }),
    getElementById: () => ({ select: vi.fn() }),
    fit: vi.fn(),
    zoom: vi.fn(() => 1),
    width: () => 500,
    height: () => 500,
    layout: () => ({ run: vi.fn() }),
  }),
}));
it("preserves correction edge semantics when replacing the theme stylesheet", () => {
  for (const palette of [
    {
      label: "#505058",
      edge: "#a8a8b2",
      selected: "#26262b",
      outline: "#f7f7f8",
    },
    {
      label: "#cdcdd3",
      edge: "#65656f",
      selected: "#d4d4d8",
      outline: "#1a1a1a",
    },
  ]) {
    const styles = graphStyle(palette);
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
    expect(styles).toHaveLength(4);
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
  fireEvent.click(
    screen.getByRole("button", { name: "Close selected memory" }),
  );
  expect(screen.queryByRole("link", { name: "Read full memory" })).toBeNull();
  await act(async () => {
    window.history.back();
  });
  await screen.findByRole("link", { name: "Read full memory" });
});
