import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { stripTypeScriptTypes } from "node:module";
import path from "node:path";
import { mock, test } from "node:test";

const current = stripTypeScriptTypes(await readFile(new URL("../../../internal/memory/setup/assets/pi/mnemon.ts", import.meta.url), "utf8"));
const before = stripTypeScriptTypes(await readFile(new URL("../../../testdata/memory/pi/mnemon-before-164.ts", import.meta.url), "utf8"));
let generation = 0;

async function load(t, { legacy = false, platform = "win32", env = {}, files = {}, links = {}, notExecutable = [], result = { status: 0, stdout: '{"total_insights":72,"edge_count":1106}' } } = {}) {
  const paths = platform === "win32" ? path.win32 : path.posix;
  const home = platform === "win32" ? "C:\\Users\\Pi 中文 home" : "/home/pi";
  const calls = [];
  mock.module("node:child_process", { namedExports: {
    spawnSync(command, args, options) { calls.push({ command, args, options }); return result; },
    execFileSync(command, args, options) {
      calls.push({ command, args, options });
      if (platform === "win32" && !Object.keys(files).some((file) => file.endsWith("mnemon.exe"))) throw new Error("ENOENT");
      return result.stdout;
    },
  } });
  mock.module("node:fs", { namedExports: {
    constants: { X_OK: 1 },
    accessSync(file) { if (notExecutable.includes(file)) throw new Error("EACCES"); },
    existsSync: (file) => Object.hasOwn(files, file),
    statSync: () => ({ isFile: () => true }),
    realpathSync: (file) => links[file] || file,
    readFileSync(file) {
      if (!Object.hasOwn(files, file)) throw new Error("ENOENT");
      if (files[file] instanceof Error) throw files[file];
      return files[file];
    },
  } });
  mock.module("node:module", { namedExports: { createRequire: () => ({ resolve(name) {
    assert.equal(name, "fixture-runtime/package.json");
    return paths.join(home, "native package", "package.json");
  } }) } });
  mock.module("node:os", { namedExports: { homedir: () => home } });
  mock.module("node:path", { defaultExport: paths, namedExports: { join: paths.join } });
  mock.module("node:process", { defaultExport: { platform, arch: "x64", env } });
  t.after(() => mock.restoreAll());
  // The unchanged old asset used global process. Bind only that dependency to
  // the same simulated host; do not alter its path/launch/guide logic.
  const source = legacy ? 'import process from "node:process";\n' + before : current;
  const extension = await import(`data:text/javascript;base64,${Buffer.from(source).toString("base64")}#${++generation}`);
  const hooks = new Map();
  extension.default({ on(name, callback) { assert.equal(hooks.has(name), false); hooks.set(name, callback); } });
  return { calls, hooks, home, paths, async content() { return (await hooks.get("before_agent_start")()).message.content; } };
}

test("Windows without HOME discovers only Mnemon and injects the Pi guide", async (t) => {
  const home = "C:\\Users\\Pi 中文 home";
  const p = await load(t, { files: {
    [path.win32.join(home, ".mnemon", "prompt", "pi", "guide.md")]: "Pi direct-memory guide",
    [path.win32.join(home, ".mnemon", "prompt", "guide.md")]: "Claude Task sonnet guide",
    [path.win32.join(home, ".pi", "agent", "skills", "mnemon", "SKILL.md")]: "Pi skill",
  } });
  assert.deepEqual(await p.hooks.get("resources_discover")(), { skillPaths: [path.win32.join(home, ".pi", "agent", "skills", "mnemon", "SKILL.md")] });
  const content = await p.content();
  assert.match(content, /72 insights, 1106 edges/);
  assert.match(content, /Pi direct-memory guide/);
  assert.doesNotMatch(content, /Task|sonnet/);
});

for (const [name, data, agent, wantAgent] of [
  ["custom", "D:\\Memory 中文 data", "D:\\Pi 中文 agent", "D:\\Pi 中文 agent"],
  ["empty", "", "", "C:\\Users\\Pi 中文 home\\.pi\\agent"],
  ["tilde", "D:\\Memory", "~/Pi 中文 agent", "C:\\Users\\Pi 中文 home\\Pi 中文 agent"],
  ["Windows tilde", "D:\\Memory", "~\\Pi 中文 agent", "C:\\Users\\Pi 中文 home\\Pi 中文 agent"],
]) {
  test(`${name} overrides agree with setup`, async (t) => {
    const root = data || "C:\\Users\\Pi 中文 home\\.mnemon";
    const p = await load(t, { env: { HOME: "", MNEMON_DATA_DIR: data, PI_CODING_AGENT_DIR: agent }, files: {
      [path.win32.join(root, "prompt", "pi", "guide.md")]: "selected Pi guide",
      [path.win32.join(wantAgent, "skills", "mnemon", "SKILL.md")]: "Pi skill",
    } });
    assert.match(await p.content(), /selected Pi guide/);
    assert.deepEqual((await p.hooks.get("resources_discover")()).skillPaths, [path.win32.join(wantAgent, "skills", "mnemon", "SKILL.md")]);
  });
}

test("local-only setup does not report a nonexistent global skill or unrelated global skills", async (t) => {
  const p = await load(t, { files: {
    "C:\\Users\\Pi 中文 home\\.pi\\agent\\skills\\other\\SKILL.md": "unrelated skill",
  } });
  assert.deepEqual(await p.hooks.get("resources_discover")(), { skillPaths: [] });
});

test("missing or unreadable scoped guide never falls back to another data root or Claude", async (t) => {
  const p = await load(t, { env: { MNEMON_DATA_DIR: "D:\\isolated" }, files: {
    "D:\\isolated\\prompt\\pi\\guide.md": new Error("EACCES"),
    "D:\\isolated\\prompt\\guide.md": "Claude Task sonnet",
    "C:\\Users\\Pi 中文 home\\.mnemon\\prompt\\pi\\guide.md": "wrong data root",
    "C:\\Users\\Pi 中文 home\\.mnemon\\prompt\\guide.md": "legacy Claude Task sonnet",
  } });
  assert.equal(await p.content(), "[mnemon] Memory active (72 insights, 1106 edges).\n\n[mnemon] Evaluate: recall needed? After responding, evaluate: remember needed?");
});

test("a missing default Pi guide never loads the legacy Claude guide", async (t) => {
  const p = await load(t, { files: {
    "C:\\Users\\Pi 中文 home\\.mnemon\\prompt\\guide.md": "legacy Claude Task sonnet",
  } });
  assert.doesNotMatch(await p.content(), /legacy|Claude|Task|sonnet/);
});

const targets = JSON.stringify([{ platform: "win32", arch: "x64", alias: "fixture-runtime", binary: "bin/mnemon.exe" }]);
for (const [name, bin, packageRoot] of [
  ["native", "C:\\native 中文 bin", undefined],
  ["npm global", "C:\\npm 中文 prefix", "C:\\npm 中文 prefix\\node_modules\\@mnemon-dev\\mnemon"],
  ["npm local", "C:\\project 中文\\node_modules\\.bin", "C:\\project 中文\\node_modules\\@mnemon-dev\\mnemon"],
]) {
  test(`Windows ${name} resolves one bounded native process without a shell`, async (t) => {
    const files = packageRoot ? {
      [path.win32.join(packageRoot, "bin", "mnemon.js")]: "launcher must be bypassed",
      [path.win32.join(packageRoot, "targets.json")]: targets,
    } : { [path.win32.join(bin, "mnemon.exe")]: "native" };
    const p = await load(t, { env: { PATH: bin }, files });
    assert.match(await p.content(), /72 insights, 1106 edges/);
    assert.equal(p.calls.length, 1);
    assert.equal(p.calls[0].command, packageRoot ? path.win32.join(p.home, "native package", "bin", "mnemon.exe") : path.win32.join(bin, "mnemon.exe"));
    assert.deepEqual(p.calls[0].args, ["status"]);
    assert.equal(p.calls[0].options.shell, undefined);
    assert.equal(p.calls[0].options.timeout, 5000);
    assert.equal(p.calls[0].options.killSignal, "SIGKILL");
    assert.equal(p.calls[0].options.maxBuffer, 256 * 1024);
    assert.equal(p.calls[0].options.windowsHide, true);
  });
}

test("Unix npm symlinks bypass the launcher", async (t) => {
  const p = await load(t, { platform: "linux", env: { PATH: "/prefix/bin" }, files: {
    "/prefix/bin/mnemon": "symlink",
    "/prefix/node_modules/@mnemon-dev/mnemon/package.json": '{"name":"@mnemon-dev/mnemon"}',
    "/prefix/node_modules/@mnemon-dev/mnemon/targets.json": JSON.stringify([{ platform: "linux", arch: "x64", alias: "fixture-runtime", binary: "bin/mnemon" }]),
  }, links: { "/prefix/bin/mnemon": "/prefix/node_modules/@mnemon-dev/mnemon/bin/mnemon.js" } });
  assert.match(await p.content(), /72 insights, 1106 edges/);
  assert.equal(p.calls[0].command, "/home/pi/native package/bin/mnemon");
});

test("a non-executable PATH entry does not shadow a later executable", async (t) => {
  const p = await load(t, { platform: "linux", env: { PATH: "/shadow:/native" }, files: {
    "/shadow/mnemon": "not executable", "/native/mnemon": "native",
  }, notExecutable: ["/shadow/mnemon"] });
  assert.match(await p.content(), /72 insights, 1106 edges/);
  assert.equal(p.calls[0].command, "/native/mnemon");
});

test("an unrelated mnemon.js executable wrapper is not an npm package", async (t) => {
  const p = await load(t, { platform: "linux", env: { PATH: "/bin" }, files: {
    "/bin/mnemon": "symlink",
  }, links: { "/bin/mnemon": "/custom/bin/mnemon.js" } });
  assert.match(await p.content(), /72 insights, 1106 edges/);
  assert.equal(p.calls[0].command, "/bin/mnemon");
});

for (const [name, result] of [
  ["missing executable", { error: new Error("ENOENT"), status: null }],
  ["failed exit", { status: 1, stdout: '{}' }],
  ["malformed JSON", { status: 0, stdout: 'invalid' }],
  ["output bound", { error: new Error("ENOBUFS"), status: null, stdout: '{"total_insights":72}' }],
]) {
  test(`${name} leaves the conversation usable`, async (t) => {
    const p = await load(t, { result });
    assert.ok((await p.content()).startsWith("[mnemon] Memory active.\n"));
  });
}

test("legacy control injects the impossible Claude workflow", async (t) => {
  const p = await load(t, { legacy: true, env: { MNEMON_DATA_DIR: "D:\\data" }, files: {
    "D:\\data\\prompt\\guide.md": "delegate to Task model=sonnet; do not write directly",
  } });
  assert.match(await p.content(), /Task model=sonnet/);
});

test("legacy control loses home paths when Windows HOME is absent", async (t) => {
  const p = await load(t, { legacy: true, files: {
    "C:\\Users\\Pi 中文 home\\.mnemon\\prompt\\guide.md": "installed guide",
  } });
  assert.doesNotMatch(await p.content(), /installed guide/);
  assert.equal(path.win32.isAbsolute((await p.hooks.get("resources_discover")()).skillPaths[0]), false);
});

test("legacy control cannot execute the Windows npm shim", async (t) => {
  const p = await load(t, { legacy: true, env: { PATH: "C:\\npm" }, files: { "C:\\npm\\mnemon.cmd": "npm shim" } });
  assert.ok((await p.content()).startsWith("[mnemon] Memory active.\n"));
  assert.equal(p.calls[0].command, "mnemon");
});

// Pi's context builder includes custom messages whatever `display` says, while
// its renderer draws only display=true. A hidden message costs the model nothing.
const piGuide = path.win32.join("C:\\Users\\Pi 中文 home", ".mnemon", "prompt", "pi", "guide.md");

// A session that records entries the way Pi does: each injected message becomes
// a custom_message entry on the current branch, and the extension may read the
// branch through ctx.sessionManager.getBranch().
function session(entries = []) {
  let next = entries.length;
  const s = {
    entries,
    add(entry) { s.entries.push({ id: `e${++next}`, ...entry }); return `e${next}`; },
    ctx: {
      ui: { setStatus() {}, notify() { throw new Error("unexpected notification"); } },
      sessionManager: { getBranch: () => s.entries },
    },
    // What /tree does: the branch is replaced, no session_start, same session id.
    navigate(branch) { s.entries = branch; },
    async turn(p) {
      s.add({ type: "message", message: { role: "user" } });
      const message = (await p.hooks.get("before_agent_start")({}, s.ctx)).message;
      s.add({ type: "custom_message", customType: message.customType, content: message.content, display: message.display, details: message.details });
      s.add({ type: "message", message: { role: "assistant" } });
      return message;
    },
  };
  return s;
}

const hasGuide = (message) => /Pi guide/.test(message.content);

test("the injected message is not drawn into the conversation", async (t) => {
  const p = await load(t, { files: { [piGuide]: "Pi guide" } });
  assert.equal((await session().turn(p)).display, false);
});

test("the full guide is injected once per conversation, then a reminder", async (t) => {
  const p = await load(t, { files: { [piGuide]: "Pi guide" } });
  const s = session();
  await p.hooks.get("session_start")({}, s.ctx);
  const first = await s.turn(p);
  assert.ok(hasGuide(first));
  assert.match(first.content, /72 insights, 1106 edges/);
  for (const again of [await s.turn(p), await s.turn(p)]) {
    assert.ok(!hasGuide(again));
    assert.match(again.content, /^\[mnemon\] Memory active/);
    assert.match(again.content, /recall/);
    assert.ok(again.content.length < first.content.length);
  }
  // A new or empty session gets the guide again.
  assert.ok(hasGuide(await session().turn(p)));
});

test("a conversation without a readable branch keeps injecting the full guide", async (t) => {
  const p = await load(t, { files: { [piGuide]: "Pi guide" } });
  for (const ctx of [undefined, {}, { sessionManager: { getBranch() { throw new Error("no session"); } } }]) {
    for (let i = 0; i < 2; i++) assert.match((await p.hooks.get("before_agent_start")({}, ctx)).message.content, /Pi guide/);
  }
});

test("the extension does not notify the user after every reply", async (t) => {
  const p = await load(t);
  assert.equal(p.hooks.has("agent_end"), false);
});

test("compaction that summarizes the guide away sends it again", async (t) => {
  const p = await load(t, { files: { [piGuide]: "Pi guide" } });
  const s = session();
  assert.ok(hasGuide(await s.turn(p)));
  assert.ok(!hasGuide(await s.turn(p)));
  // Keep only the last turn: the guide falls before firstKeptEntryId.
  const kept = s.entries[s.entries.length - 3].id;
  s.add({ type: "compaction", summary: "summary", firstKeptEntryId: kept });
  assert.ok(hasGuide(await s.turn(p)));
  assert.ok(!hasGuide(await s.turn(p)));
});

test("compaction that keeps the guide does not repeat it", async (t) => {
  const p = await load(t, { files: { [piGuide]: "Pi guide" } });
  const s = session();
  assert.ok(hasGuide(await s.turn(p)));
  s.add({ type: "compaction", summary: "summary", firstKeptEntryId: s.entries[0].id });
  assert.ok(!hasGuide(await s.turn(p)));
});

test("navigating the tree re-sends the guide only when it left the branch", async (t) => {
  const p = await load(t, { files: { [piGuide]: "Pi guide" } });
  const s = session();
  assert.ok(hasGuide(await s.turn(p)));
  const withGuide = [...s.entries];
  assert.ok(!hasGuide(await s.turn(p)));
  // /tree back to before the first turn: same session id, no session_start.
  s.navigate([]);
  assert.ok(hasGuide(await s.turn(p)));
  // /tree to a branch that still holds the guide.
  s.navigate(withGuide);
  assert.ok(!hasGuide(await s.turn(p)));
});

test("a guide message from an older extension version is replaced once", async (t) => {
  const p = await load(t, { files: { [piGuide]: "Pi guide" } });
  const s = session([{ id: "old", type: "custom_message", customType: "mnemon", content: "Pi guide", display: true }]);
  assert.ok(hasGuide(await s.turn(p)));
  assert.ok(!hasGuide(await s.turn(p)));
});

test("another extension's message is not taken for the guide", async (t) => {
  const p = await load(t, { files: { [piGuide]: "Pi guide" } });
  const s = session([{ id: "x", type: "custom_message", customType: "other", content: "Pi guide", display: false, details: { guide: true } }]);
  assert.ok(hasGuide(await s.turn(p)));
});

test("a missing guide file is retried instead of being marked as sent", async (t) => {
  const p = await load(t);
  const s = session();
  assert.equal((await s.turn(p)).details, undefined);
  assert.equal((await s.turn(p)).details, undefined);
});

test("the compaction instructions hook is kept", async (t) => {
  const p = await load(t);
  assert.match((await p.hooks.get("session_before_compact")()).customInstructions, /Before compacting/);
});
