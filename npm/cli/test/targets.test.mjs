import assert from "node:assert/strict";
import test from "node:test";

import { selectTarget, targets } from "../lib/targets.js";

test("target registry covers the complete GoReleaser matrix", () => {
  assert.equal(targets.length, 6);
  assert.deepEqual(
    targets.map(({ goos, goarch }) => `${goos}/${goarch}`).sort(),
    [
      "darwin/amd64",
      "darwin/arm64",
      "linux/amd64",
      "linux/arm64",
      "windows/amd64",
      "windows/arm64",
    ],
  );
  assert.equal(selectTarget("darwin", "arm64").id, "darwin-arm64");
  assert.equal(selectTarget("win32", "x64").goarch, "amd64");
});

test("unsupported runtimes fail closed", () => {
  assert.throws(() => selectTarget("freebsd", "x64"), /Unsupported platform/);
  assert.throws(() => selectTarget("linux", "ia32"), /Unsupported platform/);
});
