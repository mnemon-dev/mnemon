#!/usr/bin/env node

import { cp, mkdir, mkdtemp, readFile, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath, pathToFileURL } from "node:url";

import { selectTarget } from "../npm/cli/lib/targets.js";

export async function verifyNpmPackages(manifestPath) {
  const manifest = JSON.parse(await readFile(manifestPath, "utf8"));
  validateManifest(manifest);
  const npm = process.platform === "win32" ? "npm.cmd" : "npm";
  for (const artifact of manifest.packages) {
    const packed = spawnSync(npm, ["pack", artifact.directory, "--dry-run", "--json"], {
      encoding: "utf8",
    });
    if (packed.status !== 0) {
      throw new Error(`npm pack failed for ${artifact.version}: ${packed.stderr.trim()}`);
    }
    const report = JSON.parse(packed.stdout);
    const files = new Set(report[0]?.files?.map((entry) => entry.path));
    const expected =
      artifact.kind === "cli" ? "bin/mnemon.js" : nativePackageBinary(artifact.id);
    if (!files.has(expected) || !files.has("LICENSE") || !files.has("package.json")) {
      throw new Error(`npm package ${artifact.version} is missing required files`);
    }
  }
  await verifyHostLauncher(manifest);
}

function validateManifest(manifest) {
  if (manifest?.schemaVersion !== 1 || !Array.isArray(manifest.packages)) {
    throw new Error("Invalid npm package manifest");
  }
  const cli = manifest.packages.filter((artifact) => artifact.kind === "cli");
  const platforms = manifest.packages.filter((artifact) => artifact.kind === "platform");
  if (cli.length !== 1 || platforms.length !== 6 || manifest.packages.at(-1)?.kind !== "cli") {
    throw new Error("npm package manifest is incomplete or out of publish order");
  }
}

async function verifyHostLauncher(manifest) {
  const target = selectTarget(process.platform, process.arch);
  const cli = manifest.packages.find((artifact) => artifact.kind === "cli");
  const platform = manifest.packages.find(
    (artifact) => artifact.kind === "platform" && artifact.id === target.id,
  );
  if (!platform) {
    throw new Error(`npm package manifest has no host target ${target.id}`);
  }
  const temporary = await mkdtemp(path.join(os.tmpdir(), "mnemon-npm-verify-"));
  try {
    const packageRoot = path.join(temporary, "mnemon");
    await cp(cli.directory, packageRoot, { recursive: true });
    const aliasRoot = path.join(packageRoot, "node_modules", ...target.alias.split("/"));
    await mkdir(path.dirname(aliasRoot), { recursive: true });
    await cp(platform.directory, aliasRoot, { recursive: true });
    const launched = spawnSync(
      process.execPath,
      [path.join(packageRoot, "bin", "mnemon.js"), "--version"],
      { encoding: "utf8" },
    );
    if (
      launched.status !== 0 ||
      launched.stderr !== "" ||
      launched.stdout.trim() !== `mnemon version ${manifest.version}`
    ) {
      throw new Error(
        `host launcher failed: status=${launched.status} stdout=${JSON.stringify(launched.stdout)} ` +
          `stderr=${JSON.stringify(launched.stderr)}`,
      );
    }
  } finally {
    await rm(temporary, { recursive: true, force: true });
  }
}

function nativePackageBinary(id) {
  return id.startsWith("win32-") ? "bin/mnemon.exe" : "bin/mnemon";
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  const scriptDirectory = path.dirname(fileURLToPath(import.meta.url));
  const defaultManifest = path.resolve(scriptDirectory, "..", "dist", "npm", "packages.json");
  try {
    await verifyNpmPackages(path.resolve(process.argv[2] ?? defaultManifest));
    process.stdout.write("Verified npm package contents and native launcher.\n");
  } catch (error) {
    process.stderr.write(`${error.message}\n`);
    process.exitCode = 1;
  }
}
