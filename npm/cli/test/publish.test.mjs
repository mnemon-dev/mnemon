import assert from "node:assert/strict";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { publishNpmPackages } from "../../../scripts/publish-npm-packages.mjs";
import { targets } from "../lib/targets.js";

test("publish skips immutable versions and keeps the CLI package last", async (t) => {
  const temporary = await mkdtemp(path.join(os.tmpdir(), "mnemon-publish-test-"));
  t.after(() => rm(temporary, { recursive: true, force: true }));
  const packages = targets.map((target) => ({
    kind: "platform",
    id: target.id,
    name: "@mnemon-dev/mnemon",
    version: `1.2.3-${target.id}`,
    tag: target.id,
    directory: path.join(temporary, target.id),
  }));
  packages.push({
    kind: "cli",
    name: "@mnemon-dev/mnemon",
    version: "1.2.3",
    tag: "latest",
    directory: path.join(temporary, "cli"),
  });
  await mkdir(temporary, { recursive: true });
  const manifestPath = path.join(temporary, "packages.json");
  await writeFile(
    manifestPath,
    JSON.stringify({ schemaVersion: 1, version: "1.2.3", packages }),
    "utf8",
  );

  const calls = [];
  const output = [];
  const run = (_command, args) => {
    calls.push(args);
    if (args[0] === "view") {
      const version = args[1].slice(args[1].lastIndexOf("@") + 1);
      return version.endsWith("darwin-x64")
        ? { status: 0, stdout: JSON.stringify(version), stderr: "" }
        : { status: 1, stdout: "", stderr: "npm error code E404" };
    }
    return { status: 0 };
  };

  await publishNpmPackages(manifestPath, {
    run,
    stdout: { write: (value) => output.push(value) },
  });

  const publishCalls = calls.filter((args) => args[0] === "publish");
  assert.equal(publishCalls.length, 6);
  assert.equal(publishCalls.at(-1)[1], packages.at(-1).directory);
  assert.match(output.join(""), /1\.2\.3-darwin-x64; skipping/);
});

test("publish rejects a manifest whose CLI package is not last", async (t) => {
  const temporary = await mkdtemp(path.join(os.tmpdir(), "mnemon-publish-order-"));
  t.after(() => rm(temporary, { recursive: true, force: true }));
  const manifestPath = path.join(temporary, "packages.json");
  await writeFile(
    manifestPath,
    JSON.stringify({ schemaVersion: 1, packages: [{ kind: "cli" }, { kind: "platform" }] }),
    "utf8",
  );
  await assert.rejects(publishNpmPackages(manifestPath), /Invalid or unordered/);
});
