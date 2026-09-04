import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import {
  chmod,
  copyFile,
  cp,
  mkdir,
  mkdtemp,
  readFile,
  realpath,
  rm,
  writeFile,
} from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { updateNpmInstall } from "../lib/update.js";

test("update replaces the package through its owning npm prefix", async (t) => {
  const fixture = await npmFixture(t, "0.2.8");
  fixture.runner.updatedVersion = "0.2.9";
  const output = [];

  const result = await updateNpmInstall({
    packageRoot: fixture.packageRoot,
    runner: fixture.runner,
    stdout: { write: (value) => output.push(value) },
  });

  assert.deepEqual(result, { code: 0, signal: null });
  assert.deepEqual(fixture.runner.runArgs.slice(-6), [
    "install",
    "--global",
    "--prefix",
    fixture.prefix,
    "--include=optional",
    "@mnemon-dev/mnemon@latest",
  ]);
  assert.deepEqual(output, ["Updated Mnemon 0.2.8 -> 0.2.9.\n"]);
});

test("update reports an already-current installation", async (t) => {
  const fixture = await npmFixture(t, "0.2.8");
  const output = [];
  await updateNpmInstall({
    packageRoot: fixture.packageRoot,
    runner: fixture.runner,
    stdout: { write: (value) => output.push(value) },
  });
  assert.deepEqual(output, ["Mnemon is already up to date (0.2.8).\n"]);
});

test("update rejects npm from a different global installation", async (t) => {
  const fixture = await npmFixture(t, "0.2.8");
  const otherRoot = path.join(fixture.base, "other", "lib", "node_modules");
  await mkdir(path.join(otherRoot, "@mnemon-dev", "mnemon"), { recursive: true });
  fixture.runner.globalRoot = otherRoot;

  await assert.rejects(
    updateNpmInstall({ packageRoot: fixture.packageRoot, runner: fixture.runner }),
    /not managed by npm.*npm install --global @mnemon-dev\/mnemon@latest/,
  );
  assert.equal(fixture.runner.runArgs, undefined);
});

test("launcher invokes npm without keeping the native binary open", async (t) => {
  const fixture = await npmFixture(t, "0.2.8");
  const fakeBin = path.join(fixture.base, "fake-bin");
  const npmCLI = path.join(fakeBin, "node_modules", "npm", "bin", "npm-cli.js");
  await mkdir(path.dirname(npmCLI), { recursive: true });
  const implementation = [
    "#!/usr/bin/env node",
    'const {readFileSync,writeFileSync}=require("node:fs");',
    'const path=require("node:path");',
    "const args=process.argv.slice(2);",
    'if(args.join(" ")==="root --global"){console.log(process.env.MNEMON_TEST_GLOBAL);}',
    'else if(args.join(" ")==="prefix --global"){console.log(process.env.MNEMON_TEST_PREFIX);}',
    'else if(args.includes("install")){',
    ' const file=path.join(process.env.MNEMON_TEST_PACKAGE,"package.json");',
    ' const data=JSON.parse(readFileSync(file,"utf8"));',
    ' data.version="0.2.9";',
    ' writeFileSync(file,JSON.stringify(data)+"\\n");',
    "}else{process.exitCode=2;}",
    "",
  ].join("\n");
  await writeFile(npmCLI, implementation, "utf8");
  const npm = path.join(fakeBin, "npm");
  await writeFile(npm, implementation, "utf8");
  await chmod(npm, 0o755);
  await writeFile(path.join(fakeBin, "npm.cmd"), "@echo off\r\n", "utf8");

  const pathKey = Object.keys(process.env).find((name) => name.toLowerCase() === "path") ?? "PATH";
  const sourceRoot = fileURLToPath(new URL("..", import.meta.url));
  await cp(path.join(sourceRoot, "bin"), path.join(fixture.packageRoot, "bin"), {
    recursive: true,
  });
  await cp(path.join(sourceRoot, "lib"), path.join(fixture.packageRoot, "lib"), {
    recursive: true,
  });
  await copyFile(
    path.join(sourceRoot, "targets.json"),
    path.join(fixture.packageRoot, "targets.json"),
  );

  const env = {
    ...process.env,
    [pathKey]: `${fakeBin}${path.delimiter}${process.env[pathKey] ?? ""}`,
    MNEMON_TEST_GLOBAL: fixture.globalRoot,
    MNEMON_TEST_PREFIX: fixture.prefix,
    MNEMON_TEST_PACKAGE: fixture.packageRoot,
  };
  const launched = spawnSync(
    process.execPath,
    [path.join(fixture.packageRoot, "bin", "mnemon.js"), "update"],
    { encoding: "utf8", env },
  );
  assert.deepEqual(
    { status: launched.status, stdout: launched.stdout, stderr: launched.stderr },
    {
      status: 0,
      stdout: "Updated Mnemon 0.2.8 -> 0.2.9.\n",
      stderr: "",
    },
  );
});

async function npmFixture(t, version) {
  let base = await mkdtemp(path.join(os.tmpdir(), "mnemon-update-test-"));
  base = await realpath(base);
  t.after(() => rm(base, { recursive: true, force: true }));
  const prefix = path.join(base, "prefix");
  const globalRoot = path.join(prefix, "lib", "node_modules");
  const packageRoot = path.join(globalRoot, "@mnemon-dev", "mnemon");
  await writePackage(packageRoot, version);
  const runner = {
    globalRoot,
    prefix,
    updatedVersion: "",
    output(_command, args) {
      if (args.slice(-2).join(" ") === "root --global") {
        return `${this.globalRoot}\n`;
      }
      if (args.slice(-2).join(" ") === "prefix --global") {
        return `${this.prefix}\n`;
      }
      throw new Error(`unexpected query: ${args.join(" ")}`);
    },
    async run(_command, args) {
      this.runArgs = [...args];
      if (this.updatedVersion !== "") {
        await writePackage(packageRoot, this.updatedVersion);
      }
      return { code: 0, signal: null };
    },
  };
  return { base, prefix, globalRoot, packageRoot, runner };
}

async function writePackage(root, version) {
  await mkdir(root, { recursive: true });
  const existing = await readFile(path.join(root, "package.json"), "utf8").catch(() => "{}");
  const metadata = { ...JSON.parse(existing), name: "@mnemon-dev/mnemon", version };
  await writeFile(path.join(root, "package.json"), `${JSON.stringify(metadata)}\n`, "utf8");
}
