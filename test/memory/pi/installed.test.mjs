import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { chmod, copyFile, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import { executable, isolatedEnv, legacy, npmLayout, probe } from "./fixtures.mjs";

function run(command, args, cwd, env) {
  const result = spawnSync(command, args, {
    cwd, env, encoding: "utf8", timeout: 15000, killSignal: "SIGKILL",
    maxBuffer: 512 * 1024, windowsHide: true,
  });
  assert.equal(result.status, 0, `${command}: ${result.error || result.stderr}\n${result.stdout}`);
  return result.stdout;
}

for (const mode of ["native", "npm global", "npm local"]) {
  for (const custom of [false, true]) {
    test(`${mode}, ${custom ? "custom" : "default"} directories: installed extension, raw Node, real status`, async () => {
      const root = await mkdtemp(path.join(os.tmpdir(), "mnemon-pi-"));
      try {
        const home = path.join(root, "Pi 中文 home");
        const cwd = path.join(root, "project 中文 space");
        await mkdir(home); await mkdir(cwd);
        let binDir = path.join(root, "native 中文 bin");
        let bin = path.join(binDir, executable);
        await mkdir(binDir);
        await copyFile(process.env.MNEMON_TEST_BIN, bin);
        await chmod(bin, 0o755);
        if (mode.startsWith("npm")) {
          ({ bin: binDir, native: bin } = await npmLayout(path.join(root, "npm 中文 prefix"), bin, mode === "npm local"));
        }
        const env = isolatedEnv(home, binDir + path.delimiter + path.dirname(process.execPath));
        const agent = custom ? path.join(home, "custom 中文 agent") : path.join(home, ".pi", "agent");
        const data = custom ? path.join(root, "custom 中文 data") : path.join(home, ".mnemon");
        if (custom) {
          env.PI_CODING_AGENT_DIR = "~/custom 中文 agent";
          env.MNEMON_DATA_DIR = data;
        }
        if (process.platform === "win32") assert.equal(Object.hasOwn(env, "HOME"), false);
        // Both guides coexist after real CLI setup, rather than fixture writes.
        run(bin, ["setup", "--target", "claude-code", "--global", "--yes"], cwd, env);
        const shared = await readFile(path.join(data, "prompt", "guide.md"));
        run(bin, ["setup", "--target", "pi", "--global", "--yes"], cwd, env);
        assert.deepEqual(await readFile(path.join(data, "prompt", "guide.md")), shared);
        run(bin, ["remember", "Pi portability regression fact.", "--cat", "fact", "--imp", "3", "--no-diff"], cwd, env);
        const installed = path.join(agent, "extensions", "mnemon.ts");
        const actual = JSON.parse(run(process.execPath, ["--no-warnings", probe, installed], cwd, env));
        assert.match(actual.start.message.content, /Memory active \(1 insights, 0 edges\)/);
        assert.match(actual.start.message.content, /directly in the main conversation/);
        assert.doesNotMatch(actual.start.message.content, /Task|sonnet|subagent_type/);
        assert.deepEqual(actual.resources.skillPaths, [path.join(agent, "skills", "mnemon", "SKILL.md")]);
        assert.equal(actual.start.message.display, false);

        const old = JSON.parse(run(process.execPath, ["--no-warnings", probe, legacy], cwd, env));
        if (process.platform === "win32") {
          assert.equal(path.isAbsolute(old.resources.skillPaths[0]), false);
          if (!custom) assert.doesNotMatch(old.start.message.content, /Recall — before responding/);
          if (mode.startsWith("npm")) assert.ok(old.start.message.content.startsWith("[mnemon] Memory active.\n"));
        } else {
          assert.match(old.start.message.content, /Memory active \(1 insights, 0 edges\)/);
        }
        if (custom || process.platform !== "win32") assert.match(old.start.message.content, /Task.*sonnet/);

        // Eject uses the same global override and leaves other extensions intact.
        const other = path.join(agent, "extensions", "keep.ts");
        await writeFile(other, "keep");
        run(bin, ["setup", "--target", "pi", "--global", "--eject", "--yes"], cwd, env);
        await assert.rejects(readFile(installed), { code: "ENOENT" });
        assert.equal(await readFile(other, "utf8"), "keep");
        assert.deepEqual(await readFile(path.join(data, "prompt", "guide.md")), shared);
      } finally {
        await rm(root, { recursive: true, force: true });
      }
    });
  }
}
