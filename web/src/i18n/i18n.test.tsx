import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Filters } from "../Filters";
import { I18nProvider, useI18n, useT } from "./index";
import { Router } from "../router";
import type { Meta } from "../types";
import { LibraryView } from "../Views";

const meta: Meta = {
  version: "test",
  initial_store: "alpha",
  categories: ["fact"],
  edge_types: ["semantic"],
};

function LanguageSwitch() {
  const { locale, setLocale } = useI18n();
  return (
    <button onClick={() => setLocale(locale === "zh" ? "en" : "zh")}>
      switch
    </button>
  );
}

afterEach(() => {
  window.localStorage.clear();
  window.history.replaceState(null, "", "/");
  vi.unstubAllGlobals();
});

describe("i18n", () => {
  it("defaults to English for non-Chinese browsers", () => {
    expect(window.navigator.language.startsWith("zh")).toBe(false);
    render(
      <I18nProvider>
        <LanguageSwitch />
      </I18nProvider>,
    );
    expect(screen.getByRole("button", { name: "switch" })).toBeVisible();
  });

  it("follows a stored language preference before the browser language", async () => {
    window.localStorage.setItem("mnemon-language", "zh");
    window.history.replaceState(null, "", "/s/alpha/library");
    vi.stubGlobal(
      "fetch",
      vi.fn(() =>
        Promise.resolve({
          ok: true,
          status: 200,
          json: async () => ({
            items: [],
            total: 0,
            next_cursor: "",
            updated_at: "2026-10-18T09:00:00Z",
          }),
        } as Response),
      ),
    );
    render(
      <I18nProvider>
        <Router>
          <LibraryView meta={meta} refresh={0} />
        </Router>
      </I18nProvider>,
    );
    expect(await screen.findByText("记忆库已就绪")).toBeVisible();
    expect(screen.getByLabelText("搜索记忆")).toBeVisible();
  });

  it("switches languages at runtime and persists the choice", () => {
    window.history.replaceState(null, "", "/s/alpha/library");
    vi.stubGlobal(
      "fetch",
      vi.fn(() =>
        Promise.resolve({
          ok: true,
          status: 200,
          json: async () => ({
            items: [],
            total: 0,
            next_cursor: "",
            updated_at: "2026-10-18T09:00:00Z",
          }),
        } as Response),
      ),
    );
    function Probe() {
      const t = useT();
      return <p>{t("library.readyTitle")}</p>;
    }
    render(
      <I18nProvider>
        <LanguageSwitch />
        <Probe />
      </I18nProvider>,
    );
    expect(screen.getByText("Your library is ready")).toBeVisible();
    act(() => {
      fireEvent.click(screen.getByRole("button", { name: "switch" }));
    });
    expect(screen.getByText("记忆库已就绪")).toBeVisible();
    expect(window.localStorage.getItem("mnemon-language")).toBe("zh");
  });

  it("translates filter labels into Chinese", () => {
    window.localStorage.setItem("mnemon-language", "zh");
    window.history.replaceState(null, "", "/s/alpha/library");
    render(
      <I18nProvider>
        <Router>
          <Filters meta={meta} />
        </Router>
      </I18nProvider>,
    );
    expect(screen.getByText("筛选")).toBeVisible();
    expect(screen.getByRole("option", { name: "全部分类" })).toBeVisible();
    expect(screen.getByLabelText("搜索记忆")).toHaveAttribute(
      "placeholder",
      "搜索你的记忆…",
    );
  });
});
