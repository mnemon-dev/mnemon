#!/usr/bin/env node

import { readFile } from "node:fs/promises";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath, pathToFileURL } from "node:url";

import { targets } from "../npm/cli/lib/targets.js";

export async function publishNpmPackages(
  manifestPath,
  { run = spawnSync, stdout = process.stdout } = {},
) {
  const manifest = JSON.parse(await readFile(manifestPath, "utf8"));
  validateManifest(manifest);
  const npm = process.platform === "win32" ? "npm.cmd" : "npm";
  for (const artifact of manifest.packages) {
    if (packageVersionExists(run, npm, artifact.name, artifact.version)) {
      stdout.write(`Already published ${artifact.name}@${artifact.version}; skipping.\n`);
      continue;
    }
    const published = run(
      npm,
      [
        "publish",
        artifact.directory,
        "--access",
        "public",
        "--tag",
        artifact.tag,
        "--provenance",
      ],
      { stdio: "inherit" },
    );
    if (published.status !== 0) {
      const diagnostic = published.error?.message ?? `status ${published.status ?? "unknown"}`;
      throw new Error(
        `npm publish failed for ${artifact.name}@${artifact.version}: ${diagnostic}`,
      );
    }
  }
}

function validateManifest(manifest) {
  const packages = Array.isArray(manifest?.packages) ? manifest.packages : [];
  const cli = packages.filter((artifact) => artifact.kind === "cli");
  const platforms = packages.filter((artifact) => artifact.kind === "platform");
  const platformIds = new Set(platforms.map((artifact) => artifact.id));
  if (
    manifest?.schemaVersion !== 1 ||
    packages.length !== targets.length + 1 ||
    cli.length !== 1 ||
    platforms.length !== targets.length ||
    packages.at(-1)?.kind !== "cli" ||
    targets.some((target) => !platformIds.has(target.id))
  ) {
    throw new Error("Invalid or unordered npm package manifest");
  }
}

function packageVersionExists(run, npm, name, version) {
  const inspected = run(npm, ["view", `${name}@${version}`, "version", "--json"], {
    encoding: "utf8",
  });
  if (inspected.status === 0) {
    return JSON.parse(inspected.stdout) === version;
  }
  const diagnostic = `${inspected.stdout}\n${inspected.stderr}\n${inspected.error?.message ?? ""}`;
  if (/\bE404\b|code E404/i.test(diagnostic)) {
    return false;
  }
  throw new Error(`Could not inspect ${name}@${version}: ${diagnostic.trim()}`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  const scriptDirectory = path.dirname(fileURLToPath(import.meta.url));
  const defaultManifest = path.resolve(scriptDirectory, "..", "dist", "npm", "packages.json");
  try {
    await publishNpmPackages(path.resolve(process.argv[2] ?? defaultManifest));
  } catch (error) {
    process.stderr.write(`${error.message}\n`);
    process.exitCode = 1;
  }
}
