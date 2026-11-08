import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { APIError } from "./api";
import { Category, Markdown, QueryView } from "./components";
import { I18nProvider } from "./i18n";

describe("private Markdown reader", () => {
  it("renders Chinese, GFM tables, code, and safe links without creating HTML or remote image requests", () => {
    const { container } = render(
      <I18nProvider>
        <Markdown
          content={
            '# 记忆与 context\n\n**Keep this** and `literal code`.\n\n| Name | Value |\n| --- | --- |\n| 项目 | Mnemon |\n\n![tracking pixel](https://tracker.invalid/pixel.gif)\n\n<script>alert("bad")</script>\n\n<img src="https://tracker.invalid/raw" onerror="alert(1)" />\n\n[Unsafe](javascript:alert%281%29) [Reference](https://example.com)'
          }
        />
      </I18nProvider>,
    );
    expect(
      screen.getByRole("heading", { name: "记忆与 context" }),
    ).toBeVisible();
    expect(screen.getByRole("table")).toHaveTextContent("项目");
    expect(container.querySelector("code")).toHaveTextContent("literal code");
    expect(container.querySelector("img,script,iframe")).toBeNull();
    expect(
      screen.getByText(/Image hidden for privacy: tracking pixel/),
    ).toBeVisible();
    expect(screen.getByText("Unsafe")).not.toHaveAttribute(
      "href",
      expect.stringContaining("javascript:"),
    );
    expect(screen.getByRole("link", { name: "Reference" })).toHaveAttribute(
      "rel",
      "noopener noreferrer",
    );
  });
});

describe("request states", () => {
  it("shows an unavailable reader and retry instead of rendering absent memory data", () => {
    const retry = vi.fn();
    render(
      <I18nProvider>
        <QueryView
          unavailable
          query={{
            key: "/missing",
            loading: false,
            loadingMore: false,
            error: new APIError("Memory no longer exists", 404),
            refresh: retry,
            loadMore: vi.fn(),
          }}
        >
          {() => <p>Secret old content</p>}
        </QueryView>
      </I18nProvider>,
    );
    expect(
      screen.getByRole("heading", { name: "Memory unavailable" }),
    ).toBeVisible();
    expect(screen.queryByText("Secret old content")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(retry).toHaveBeenCalledOnce();
  });
  it("labels retained same-scope data as stale with a usable retry action", () => {
    const retry = vi.fn();
    render(
      <I18nProvider>
        <QueryView
          query={{
            key: "/same",
            data: { content: "Saved result" },
            error: new Error("Connection lost"),
            loading: false,
            loadingMore: false,
            refresh: retry,
            loadMore: vi.fn(),
          }}
        >
          {(data) => <p>{data.content}</p>}
        </QueryView>
      </I18nProvider>,
    );
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Showing previously loaded data.",
    );
    expect(screen.getByText("Saved result")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(retry).toHaveBeenCalledOnce();
  });
  it("renders unknown authoritative categories with a neutral fallback", () => {
    const { container } = render(
      <I18nProvider>
        <Category value="new_category" />
      </I18nProvider>,
    );
    expect(screen.getByText("New category")).toBeVisible();
    expect(container.querySelector(".category-dot")).toHaveStyle({
      background: "#93918c",
    });
  });
});
