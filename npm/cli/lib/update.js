import { constants } from "node:fs";
import { access, readFile, realpath } from "node:fs/promises";
import path from "node:path";
import { spawnSync } from "node:child_process";

import { runChild } from "./child.js";

const packageName = "@mnemon-dev/mnemon";

export async function updateNpmInstall({
  packageRoot,
  runner = productionRunner(),
  stdout = process.stdout,
}) {
  const currentRoot = await canonicalPath(packageRoot);
  const current = await readPackage(currentRoot);
  const npm = await resolveNpmInvocation();

  const globalRoot = await npmPath(runner, npm, ["root", "--global"]);
  let expectedRoot;
  try {
    expectedRoot = await canonicalPath(path.join(globalRoot, ...packageName.split("/")));
  } catch (error) {
    throw notManagedError(`cannot resolve the package in npm's global root: ${error.message}`);
  }
  if (!samePath(expectedRoot, currentRoot)) {
    throw notManagedError("npm on PATH owns a different global installation");
  }

  const prefix = await npmPath(runner, npm, ["prefix", "--global"]);
  if (!pathWithin(prefix, globalRoot)) {
    throw notManagedError("npm's global package root is outside its prefix");
  }

  const result = await runner.run(npm.command, [
    ...npm.args,
    "install",
    "--global",
    "--prefix",
    prefix,
    "--include=optional",
    `${packageName}@latest`,
  ]);
  if (result.signal) {
    return result;
  }
  if (result.code !== 0) {
    throw new Error(`npm install exited with status ${result.code ?? "unknown"}`);
  }

  const updated = await readPackage(currentRoot);
  if (current.version === updated.version) {
    stdout.write(`Mnemon is already up to date (${updated.version}).\n`);
  } else {
    stdout.write(`Updated Mnemon ${current.version} -> ${updated.version}.\n`);
  }
  return { code: 0, signal: null };
}

function productionRunner() {
  return {
    output(command, args) {
      const result = spawnSync(command, args, { encoding: "utf8" });
      if (result.error) {
        throw result.error;
      }
      if (result.status !== 0) {
        throw new Error(
          `${command} ${args.join(" ")} exited with status ${result.status ?? "unknown"}: ` +
            (result.stderr ?? "").trim(),
        );
      }
      return result.stdout;
    },
    run(command, args) {
      return runChild(command, args);
    },
  };
}

async function npmPath(runner, npm, args) {
  try {
    const value = runner.output(npm.command, [...npm.args, ...args]).trim();
    return await canonicalPath(value);
  } catch (error) {
    throw new Error(`cannot inspect npm ${args[0]}: ${error.message}`);
  }
}

async function resolveNpmInvocation() {
  if (process.platform !== "win32") {
    return { command: "npm", args: [] };
  }

  const pathValue = Object.entries(process.env).find(
    ([name]) => name.toLowerCase() === "path",
  )?.[1];
  for (let entry of pathValue?.split(path.delimiter) ?? []) {
    if (entry.startsWith('"') && entry.endsWith('"')) {
      entry = entry.slice(1, -1);
    }
    if (entry === "") {
      continue;
    }
    const npmExecutable = path.join(entry, "npm.exe");
    try {
      await access(npmExecutable, constants.F_OK);
      return { command: npmExecutable, args: [] };
    } catch {
      // Standard Node.js installations expose npm through npm.cmd instead.
    }
    const npmCommand = path.join(entry, "npm.cmd");
    const npmCLI = path.join(entry, "node_modules", "npm", "bin", "npm-cli.js");
    try {
      await Promise.all([
        access(npmCommand, constants.F_OK),
        access(npmCLI, constants.F_OK),
      ]);
      // A .cmd file cannot be spawned directly without a shell. Invoke npm's
      // JavaScript entry point with the already-running Node executable.
      return { command: process.execPath, args: [npmCLI] };
    } catch {
      // Keep searching PATH for a complete Node.js/npm installation.
    }
  }
  throw new Error("npm is required to update Mnemon but was not found on PATH");
}

async function canonicalPath(value) {
  if (typeof value !== "string" || value.trim() !== value || !path.isAbsolute(value)) {
    throw new Error("path is not absolute and clean");
  }
  const cleaned = path.normalize(value);
  if (cleaned !== value) {
    throw new Error("path is not absolute and clean");
  }
  return realpath(value);
}

async function readPackage(root) {
  let metadata;
  try {
    metadata = JSON.parse(await readFile(path.join(root, "package.json"), "utf8"));
  } catch (error) {
    throw notManagedError(`cannot read package metadata: ${error.message}`);
  }
  if (
    metadata.name !== packageName ||
    typeof metadata.version !== "string" ||
    metadata.version.trim() === ""
  ) {
    throw notManagedError("package name or version is invalid");
  }
  return metadata;
}

function pathWithin(root, candidate) {
  const relative = path.relative(root, candidate);
  return (
    relative !== "" &&
    relative !== ".." &&
    !relative.startsWith(`..${path.sep}`) &&
    !path.isAbsolute(relative)
  );
}

function samePath(left, right) {
  return process.platform === "win32"
    ? left.toLowerCase() === right.toLowerCase()
    : left === right;
}

function notManagedError(reason) {
  return new Error(
    `this Mnemon installation is not managed by npm: ${reason}; ` +
      `migrate once with: npm install --global ${packageName}@latest`,
  );
}
