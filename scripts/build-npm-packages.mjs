#!/usr/bin/env node

import { chmod, copyFile, cp, mkdir, readFile, realpath, rm, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

import { targets } from "../npm/cli/lib/targets.js";

const packageName = "@mnemon-dev/mnemon";
const scriptDirectory = path.dirname(fileURLToPath(import.meta.url));
const defaultRepositoryRoot = path.resolve(scriptDirectory, "..");

export async function stageNpmPackages({
  version,
  distDirectory,
  outputDirectory,
  repositoryRoot = defaultRepositoryRoot,
}) {
  const normalizedVersion = normalizeVersion(version);
  const distRoot = await realpath(path.resolve(distDirectory));
  const requestedOutput = path.resolve(outputDirectory);
  const outputRoot = path.join(
    await realpath(path.dirname(requestedOutput)),
    path.basename(requestedOutput),
  );
  requireChildPath(distRoot, outputRoot, "npm output directory");

  const artifacts = JSON.parse(await readFile(path.join(distRoot, "artifacts.json"), "utf8"));
  if (!Array.isArray(artifacts)) {
    throw new Error("GoReleaser artifacts.json must contain an array");
  }

  await rm(outputRoot, { recursive: true, force: true });
  await mkdir(outputRoot, { recursive: true });

  const staged = [];
  for (const target of targets) {
    const artifact = selectBinaryArtifact(artifacts, target);
    const source = await resolveArtifactPath(distRoot, artifact.path);
    const platformVersion = `${normalizedVersion}-${target.id}`;
    const directory = path.join(outputRoot, target.id);
    await mkdir(path.join(directory, "bin"), { recursive: true });
    await copyFile(source, path.join(directory, target.binary));
    if (target.platform !== "win32") {
      await chmod(path.join(directory, target.binary), 0o755);
    }
    await copyFile(path.join(repositoryRoot, "LICENSE"), path.join(directory, "LICENSE"));
    await writeJSON(path.join(directory, "package.json"), platformPackage(target, platformVersion));
    await writeFile(
      path.join(directory, "README.md"),
      platformReadme(target, platformVersion),
      "utf8",
    );
    staged.push({
      kind: "platform",
      id: target.id,
      alias: target.alias,
      name: packageName,
      version: platformVersion,
      tag: platformTag(normalizedVersion, target.id),
      directory,
    });
  }

  const cliDirectory = path.join(outputRoot, "cli");
  await copyCliSource(repositoryRoot, cliDirectory);
  const cliPackagePath = path.join(cliDirectory, "package.json");
  const cliPackage = JSON.parse(await readFile(cliPackagePath, "utf8"));
  delete cliPackage.private;
  cliPackage.version = normalizedVersion;
  cliPackage.optionalDependencies = Object.fromEntries(
    targets.map((target) => [
      target.alias,
      `npm:${packageName}@${normalizedVersion}-${target.id}`,
    ]),
  );
  await writeJSON(cliPackagePath, cliPackage);
  await copyFile(path.join(repositoryRoot, "LICENSE"), path.join(cliDirectory, "LICENSE"));
  staged.push({
    kind: "cli",
    name: packageName,
    version: normalizedVersion,
    tag: releaseTag(normalizedVersion),
    directory: cliDirectory,
  });

  const manifest = { schemaVersion: 1, version: normalizedVersion, packages: staged };
  await writeJSON(path.join(outputRoot, "packages.json"), manifest);
  return manifest;
}

export function normalizeVersion(version) {
  const normalized = String(version ?? "").replace(/^v/, "");
  const semanticVersion =
    /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/;
  if (!semanticVersion.test(normalized)) {
    throw new Error(`Invalid npm release version: ${version}`);
  }
  return normalized;
}

export function releaseTag(version) {
  return version.includes("-") ? "next" : "latest";
}

function platformTag(version, id) {
  const channel = releaseTag(version);
  return channel === "latest" ? id : `${channel}-${id}`;
}

function selectBinaryArtifact(artifacts, target) {
  const matches = artifacts.filter(
    (artifact) =>
      artifact?.type === "Binary" &&
      artifact?.extra?.ID === "mnemon" &&
      artifact?.goos === target.goos &&
      artifact?.goarch === target.goarch,
  );
  if (matches.length !== 1) {
    throw new Error(
      `Expected one Mnemon binary for ${target.goos}/${target.goarch}, found ${matches.length}`,
    );
  }
  return matches[0];
}

async function resolveArtifactPath(distRoot, artifactPath) {
  if (typeof artifactPath !== "string" || artifactPath.length === 0) {
    throw new Error("GoReleaser binary artifact has no path");
  }
  let candidate = artifactPath;
  if (!path.isAbsolute(candidate)) {
    candidate = candidate.split(path.sep)[0] === path.basename(distRoot)
      ? path.resolve(path.dirname(distRoot), candidate)
      : path.resolve(distRoot, candidate);
  }
  let resolved;
  try {
    resolved = await realpath(candidate);
  } catch (error) {
    if (error.code === "ENOENT") {
      throw new Error(`GoReleaser binary does not exist: ${artifactPath}`);
    }
    throw error;
  }
  requireChildPath(distRoot, resolved, "GoReleaser artifact");
  return resolved;
}

function requireChildPath(root, candidate, label) {
  const relative = path.relative(root, candidate);
  if (relative === "" || relative === ".." || relative.startsWith(`..${path.sep}`)) {
    throw new Error(`${label} must be inside ${root}`);
  }
}

function platformPackage(target, version) {
  return {
    name: packageName,
    version,
    description: `Mnemon native binary for ${target.platform}/${target.arch}`,
    os: [target.platform],
    cpu: [target.arch],
    files: ["bin", "README.md", "LICENSE"],
    repository: {
      type: "git",
      url: "git+https://github.com/mnemon-dev/mnemon.git",
    },
    homepage: "https://github.com/mnemon-dev/mnemon",
    license: "Apache-2.0",
    publishConfig: { access: "public" },
  };
}

function platformReadme(target, version) {
  return `# Mnemon ${target.id}\n\n` +
    `Native ${target.platform}/${target.arch} artifact for ` +
    `\`${packageName}@${version}\`. Install \`${packageName}\` instead of this ` +
    `platform artifact directly.\n`;
}

async function copyCliSource(repositoryRoot, destination) {
  const source = path.join(repositoryRoot, "npm", "cli");
  await mkdir(destination, { recursive: true });
  for (const entry of ["bin", "lib"]) {
    await cp(path.join(source, entry), path.join(destination, entry), { recursive: true });
  }
  for (const entry of ["package.json", "targets.json", "README.md"]) {
    await copyFile(path.join(source, entry), path.join(destination, entry));
  }
}

async function writeJSON(file, value) {
  await writeFile(file, `${JSON.stringify(value, null, 2)}\n`, "utf8");
}

function parseArguments(argv) {
  const values = {};
  for (let index = 0; index < argv.length; index += 2) {
    const flag = argv[index];
    const value = argv[index + 1];
    if (!["--version", "--dist", "--output"].includes(flag) || value === undefined) {
      throw new Error("Usage: build-npm-packages --version <version> --dist <dir> --output <dir>");
    }
    values[flag.slice(2)] = value;
  }
  if (!values.version || !values.dist || !values.output) {
    throw new Error("Usage: build-npm-packages --version <version> --dist <dir> --output <dir>");
  }
  return values;
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  try {
    const args = parseArguments(process.argv.slice(2));
    const manifest = await stageNpmPackages({
      version: args.version,
      distDirectory: args.dist,
      outputDirectory: args.output,
    });
    process.stdout.write(
      `Staged ${manifest.packages.length} npm artifacts for ${manifest.version}.\n`,
    );
  } catch (error) {
    process.stderr.write(`${error.message}\n`);
    process.exitCode = 1;
  }
}
