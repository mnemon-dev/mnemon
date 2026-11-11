import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach, vi } from "vitest";

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});
Object.defineProperty(window, "scrollTo", { value: vi.fn(), writable: true });
HTMLElement.prototype.scrollIntoView = vi.fn();
