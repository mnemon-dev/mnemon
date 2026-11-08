import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { I18nProvider } from "./i18n";
import { Router } from "./router";
import { ActivityView, LibraryView, ReaderView } from "./Views";
import type { Insight, Meta } from "./types";

const memory: Insight = {
  id: "m1",
  content: "# Preserve the source\n\n记住 **the original**.",
  category: "fact",
  importance: 4,
  tags: ["local"],
  entities: ["Mnemon"],
  source: "custom-producer/string",
  access_count: 12,
  created_at: "2026-10-18T09:00:00Z",
  updated_at: "2026-10-18T09:00:00Z",
};
const meta: Meta = {
  version: "test",
  initial_store: "alpha",
  categories: ["fact", "new_category"],
  edge_types: ["semantic", "supersedes"],
};
const json = (body: unknown) =>
  Promise.resolve({
    ok: true,
    status: 200,
    json: async () => body,
  } as Response);

it("renders faithful source and correction direction with filter-preserving reader navigation", async () => {
  window.history.replaceState(
    null,
    "",
    "/s/alpha/memories/m1?q=remember&entity=Mnemon",
  );
  vi.stubGlobal(
    "fetch",
    vi.fn(() =>
      json({
        memory,
        related: [],
        edges: [],
        superseded_by: ["newer"],
        replaces: ["older"],
        truncated: false,
        updated_at: memory.updated_at,
      }),
    ),
  );
  render(
    <I18nProvider>
      <Router>
        <ReaderView refresh={0} />
      </Router>
    </I18nProvider>,
  );
  await screen.findByRole("heading", { level: 1, name: "Preserve the source" });
  expect(screen.getAllByText("custom-producer/string")).toHaveLength(2);
  expect(screen.getByText("This memory has been replaced.")).toBeVisible();
  expect(
    screen.getByRole("heading", { name: "Replaces" }).nextElementSibling,
  ).toHaveTextContent("corrects or supersedes");
  expect(screen.getByRole("link", { name: "older" })).toHaveAttribute(
    "href",
    "/s/alpha/memories/older?q=remember&entity=Mnemon",
  );
  expect(screen.getByRole("link", { name: "Back to library" })).toHaveAttribute(
    "href",
    "/s/alpha/library?q=remember&entity=Mnemon",
  );
  expect(
    screen.getByRole("link", { name: "Explore neighborhood" }),
  ).toHaveAttribute("href", "/s/alpha/graph?focus=m1");
});

it("shows unavailable activity as plain IDs and only links available memories", async () => {
  window.history.replaceState(null, "", "/s/alpha/activity");
  vi.stubGlobal(
    "fetch",
    vi.fn(() =>
      json({
        items: [
          {
            id: "op1",
            operation: "forget",
            insight_id: "deleted",
            available: false,
            created_at: memory.created_at,
          },
          {
            id: "op2",
            operation: "remember",
            insight_id: "m1",
            available: true,
            created_at: memory.created_at,
          },
        ],
        next_cursor: "",
        updated_at: memory.updated_at,
      }),
    ),
  );
  render(
    <I18nProvider>
      <Router>
        <ActivityView refresh={0} />
      </Router>
    </I18nProvider>,
  );
  expect(await screen.findByText("deleted")).toBeVisible();
  expect(screen.queryByRole("link", { name: "deleted" })).toBeNull();
  expect(screen.getByRole("link", { name: "m1" })).toHaveAttribute(
    "href",
    "/s/alpha/memories/m1",
  );
});

it("uses authoritative category options and submits search and date filters through the URL", async () => {
  window.history.replaceState(null, "", "/s/alpha/library");
  const fetcher = vi.fn((url: string) =>
    json(
      url.includes("/overview")
        ? { entities: [], sources: [], tags: [] }
        : {
            items: [],
            total: 0,
            next_cursor: "",
            updated_at: memory.updated_at,
          },
    ),
  );
  vi.stubGlobal("fetch", fetcher);
  render(
    <I18nProvider>
      <Router>
        <LibraryView meta={meta} refresh={0} />
      </Router>
    </I18nProvider>,
  );
  await screen.findByText("Your library is ready");
  expect(screen.getByRole("option", { name: "New category" })).toBeVisible();
  fireEvent.change(screen.getByLabelText("Search memories"), {
    target: { value: "中文" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Submit search" }));
  await screen.findByText("No matching memories");
  expect(new URLSearchParams(window.location.search).get("q")).toBe("中文");
  fireEvent.click(screen.getByRole("button", { name: "Filters" }));
  fireEvent.change(screen.getByLabelText("Since"), {
    target: { value: "2026-10-01" },
  });
  await waitFor(() =>
    expect(new URLSearchParams(window.location.search).get("since")).toBe(
      "2026-10-01",
    ),
  );
  expect(
    fetcher.mock.calls.some(([url]) => url.includes("since=2026-10-01")),
  ).toBe(true);
});

it("clears visible content and facet metadata when the library changes stores", async () => {
  window.history.replaceState(null, "", "/s/alpha/library");
  let resolveBeta!: (response: Response) => void;
  const pendingBeta = new Promise<Response>((resolve) => {
    resolveBeta = resolve;
  });
  vi.stubGlobal(
    "fetch",
    vi.fn((url: string) => {
      if (url.includes("/beta/")) return pendingBeta;
      return json(
        url.includes("/overview")
          ? {
              entities: [{ name: "Private entity", count: 1 }],
              sources: ["Private source"],
              tags: ["Private tag"],
            }
          : {
              items: [memory],
              total: 1,
              next_cursor: "",
              updated_at: memory.updated_at,
            },
      );
    }),
  );
  render(
    <I18nProvider>
      <Router>
        <LibraryView meta={meta} refresh={0} />
      </Router>
    </I18nProvider>,
  );
  await screen.findByRole("heading", { name: "Preserve the source" });
  fireEvent.click(screen.getByRole("button", { name: "Filters" }));
  expect(
    document.querySelector('option[value="Private source"]'),
  ).not.toBeNull();
  act(() => {
    window.history.pushState(null, "", "/s/beta/library");
    window.dispatchEvent(new PopStateEvent("popstate"));
  });
  expect(screen.queryByText("Preserve the source")).toBeNull();
  expect(document.querySelector('option[value="Private source"]')).toBeNull();
  expect(document.querySelector('option[value="Private entity"]')).toBeNull();
  expect(document.querySelector('option[value="Private tag"]')).toBeNull();
  await act(async () => {
    resolveBeta({
      ok: true,
      status: 200,
      json: async () => ({
        items: [],
        total: 0,
        entities: [],
        sources: [],
        tags: [],
        next_cursor: "",
        updated_at: memory.updated_at,
      }),
    } as Response);
  });
});
