import assert from "node:assert/strict";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import {
  normalizeVersion,
  releaseTag,
  stageNpmPackages,
} from "../../../scripts/build-npm-packages.mjs";
import { targets } from "../lib/targets.js";

test("release versions and dist tags are deterministic", () => {
  assert.equal(normalizeVersion("v1.2.3"), "1.2.3");
  assert.equal(normalizeVersion("1.2.3-rc.1"), "1.2.3-rc.1");
  assert.equal(releaseTag("1.2.3"), "latest");
  assert.equal(releaseTag("1.2.3-rc.1"), "next");
  assert.throws(() => normalizeVersion("latest"), /Invalid npm release version/);
});

test("GoReleaser binaries become pinned npm platform aliases", async (t) => {
  const temporary = await mkdtemp(path.join(os.tmpdir(), "mnemon-npm-packages-"));
  t.after(() => rm(temporary, { recursive: true, force: true }));
  const dist = path.join(temporary, "dist");
  const artifacts = [];
  await mkdir(dist, { recursive: true });
  for (const target of targets) {
    const binary = path.join(dist, target.id, path.basename(target.binary));
    await mkdir(path.dirname(binary), { recursive: true });
    await writeFile(binary, `binary:${target.id}`, { mode: 0o755 });
    artifacts.push({
      type: "Binary",
      goos: target.goos,
      goarch: target.goarch,
      path: binary,
      extra: { ID: "mnemon" },
    });
  }
  await writeFile(path.join(dist, "artifacts.json"), JSON.stringify(artifacts), "utf8");
  const output = path.join(dist, "npm");
  const manifest = await stageNpmPackages({
    version: "v1.2.3",
    distDirectory: dist,
    outputDirectory: output,
  });

  assert.equal(manifest.packages.length, 7);
  assert.equal(manifest.packages.at(-1).kind, "cli");
  const cli = JSON.parse(await readFile(path.join(output, "cli", "package.json"), "utf8"));
  assert.equal(cli.version, "1.2.3");
  assert.equal(cli.private, undefined);
  assert.equal(
    cli.optionalDependencies["@mnemon-dev/mnemon-darwin-arm64"],
    "npm:@mnemon-dev/mnemon@1.2.3-darwin-arm64",
  );
  const platform = JSON.parse(
    await readFile(path.join(output, "linux-x64", "package.json"), "utf8"),
  );
  assert.equal(platform.name, "@mnemon-dev/mnemon");
  assert.equal(platform.version, "1.2.3-linux-x64");
  assert.deepEqual(platform.os, ["linux"]);
  assert.deepEqual(platform.cpu, ["x64"]);
  assert.equal(
    await readFile(path.join(output, "linux-x64", "bin", "mnemon"), "utf8"),
    "binary:linux-x64",
  );
});

test("artifact paths outside GoReleaser dist fail closed", async (t) => {
  const temporary = await mkdtemp(path.join(os.tmpdir(), "mnemon-npm-escape-"));
  t.after(() => rm(temporary, { recursive: true, force: true }));
  const dist = path.join(temporary, "dist");
  const outside = path.join(temporary, "mnemon");
  await mkdir(dist, { recursive: true });
  await writeFile(outside, "binary", { mode: 0o755 });
  const artifacts = targets.map((target) => ({
    type: "Binary",
    goos: target.goos,
    goarch: target.goarch,
    path: outside,
    extra: { ID: "mnemon" },
  }));
  await writeFile(path.join(dist, "artifacts.json"), JSON.stringify(artifacts), "utf8");
  await assert.rejects(
    stageNpmPackages({
      version: "1.2.3",
      distDirectory: dist,
      outputDirectory: path.join(dist, "npm"),
    }),
    /GoReleaser artifact must be inside/,
  );
});
