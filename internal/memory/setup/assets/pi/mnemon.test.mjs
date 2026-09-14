import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, dirname, isAbsolute, join, resolve } from "node:path";
import test from "node:test";
import { pathToFileURL } from "node:url";
import memoryExtension from "./mnemon.ts";

// An explicit pinned SDK keeps this opt-in boundary test out of regular Go CI.
const packageDir = process.env.PI_MEMORY_PACKAGE_DIR;
assert.ok(packageDir && isAbsolute(packageDir), "Set PI_MEMORY_PACKAGE_DIR to the installed @earendil-works/pi-coding-agent@0.83.0 directory");
assert.equal(JSON.parse(readFileSync(join(packageDir, "package.json"), "utf8")).version, "0.83.0");
const sdk = await import(pathToFileURL(join(packageDir, "dist/index.js")));
// npm's published shrinkwrap keeps the matching provider dependency here.
const ai = await import(pathToFileURL(join(packageDir, "node_modules/@earendil-works/pi-ai/dist/index.js")));
const binary = resolve(process.env.MNEMON_BIN || join(import.meta.dirname, "../../../../../mnemon"));
const guide = readFileSync(new URL("./guide.md", import.meta.url), "utf8");
const GUIDE_MARKER = "### Mnemon memory in Pi";

function scopedEnvironment(t, values) {
  const previous = new Map(Object.keys(values).map((key) => [key, process.env[key]]));
  Object.assign(process.env, values);
  t.after(() => {
    for (const [key, value] of previous) {
      if (value === undefined) delete process.env[key];
      else process.env[key] = value;
    }
  });
}

async function createFixture(t) {
  const root = mkdtempSync(join(process.env.PI_MEMORY_TEST_TMPDIR || tmpdir(), "mnemon-pi-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const cwd = join(root, "work");
  const agentDir = join(root, "pi");
  const dataDir = join(root, "memory");
  const guidePath = join(dataDir, "prompt/pi/guide.md");
  for (const path of [cwd, agentDir, dirname(guidePath)]) mkdirSync(path, { recursive: true });
  writeFileSync(guidePath, guide);
  writeFileSync(join(dataDir, "prompt/guide.md"), "CLAUDE_SHARED_GUIDE_SENTINEL");
  scopedEnvironment(t, {
    MNEMON_DATA_DIR: dataDir,
    MNEMON_STORE: "pi-scope",
    MNEMON_EMBED_ENDPOINT: "http://127.0.0.1:1",
    MNEMON_EMBED_PROTOCOL: "ollama",
    PI_CODING_AGENT_DIR: agentDir,
    PATH: `${dirname(binary)}${delimiter}${process.env.PATH || ""}`,
  });
  // Seed only private fixture stores, with no external embedding endpoint.
  const cliEnv = {
    PATH: process.env.PATH,
    MNEMON_DATA_DIR: dataDir,
    MNEMON_STORE: "pi-scope",
    MNEMON_EMBED_ENDPOINT: "http://127.0.0.1:1",
    MNEMON_EMBED_PROTOCOL: "ollama",
  };
  const run = (...args) => JSON.parse(execFileSync(binary, args, { env: cliEnv, encoding: "utf8" }));
  run("remember", "Private Pi fixture preference", "--cat", "preference", "--source", "test");
  run("remember", "Decoy store fact", "--store", "decoy", "--source", "test");
  run("remember", "Another decoy store fact", "--store", "decoy", "--source", "test");
  // The active file points elsewhere: the inherited MNEMON_STORE must win.
  execFileSync(binary, ["store", "set", "decoy"], { env: cliEnv, encoding: "utf8" });
  const activeBefore = readFileSync(join(dataDir, "active"), "utf8");
  const calls = [];
  let phase = "turns";
  let starts = 0;
  const cost = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 };
  const usage = { input: 100, output: 5, cacheRead: 0, cacheWrite: 0, totalTokens: 105, cost };
  const provider = (pi) => {
    pi.on("before_agent_start", () => { starts++; });
    pi.registerProvider("mnemon-memory-offline", {
      api: "openai-completions",
      apiKey: "offline-not-a-secret",
      baseUrl: "http://127.0.0.1:1",
      models: [{ id: "offline", name: "Offline Memory Oracle", reasoning: false, input: ["text"], cost,
        contextWindow: 131072, maxTokens: 4096 }],
      streamSimple(model, context) {
        const stream = ai.createAssistantMessageEventStream();
        const json = JSON.stringify(context);
        calls.push({ phase, json, guideCopies: json.split(GUIDE_MARKER).length - 1, tools: context.tools || [] });
        queueMicrotask(() => {
          const message = {
            role: "assistant", content: [{ type: "text", text: phase === "compact" ? "Offline summary." : "Offline answer." }],
            api: model.api, provider: model.provider, model: model.id, usage, stopReason: "stop", timestamp: Date.now(),
          };
          stream.push({ type: "start", partial: message });
          stream.push({ type: "done", reason: "stop", message });
        });
        return stream;
      },
    });
  };
  const settingsManager = sdk.SettingsManager.inMemory({
    compaction: { enabled: false, reserveTokens: 1024, keepRecentTokens: 256 }, retry: { enabled: false },
  });
  const resourceLoader = new sdk.DefaultResourceLoader({
    cwd, agentDir, settingsManager, noExtensions: true, noSkills: true, noPromptTemplates: true,
    noThemes: true, noContextFiles: true, systemPrompt: "Offline Memory Test.",
    extensionFactories: [provider, memoryExtension],
  });
  await resourceLoader.reload();
  const modelRuntime = await sdk.ModelRuntime.create({ authPath: join(agentDir, "auth.json"), modelsPath: join(agentDir, "models.json") });
  const model = { api: "openai-completions", provider: "mnemon-memory-offline", id: "offline", name: "Offline Memory Oracle",
    baseUrl: "http://127.0.0.1:1", reasoning: false, input: ["text"], cost, contextWindow: 131072, maxTokens: 4096 };
  const { session, extensionsResult } = await sdk.createAgentSession({
    cwd, agentDir, model, modelRuntime, resourceLoader, settingsManager, thinkingLevel: "off",
    sessionManager: sdk.SessionManager.inMemory(cwd),
  });
  t.after(() => session.dispose());
  assert.deepEqual(extensionsResult.errors, []);
  return { session, calls, dataDir, guidePath, activeBefore, run, getStarts: () => starts, setPhase: value => { phase = value; } };
}

test("Pi guide stays per turn, preserves scope, and refreshes context after compaction", async (t) => {
  const f = await createFixture(t);
  const { session, calls } = f;
  await session.sendCustomMessage({ customType: "mnemon", content: "MNEMON_LEGACY_SENTINEL", display: false });
  await session.sendCustomMessage({ customType: "other-extension", content: "OTHER_EXTENSION_SENTINEL", display: false });
  for (let i = 0; i < 25; i++) await session.prompt(`Offline continuity turn ${i}.`);
  assert.equal(calls.length, 25);
  assert.deepEqual(session.getActiveToolNames(), ["read", "bash", "edit", "write"]);
  for (const call of calls) {
    assert.equal(call.guideCopies, 1);
    assert.ok(!call.json.includes("MNEMON_LEGACY_SENTINEL"));
    assert.ok(call.json.includes("OTHER_EXTENSION_SENTINEL"));
    assert.ok(!call.json.includes("CLAUDE_SHARED_GUIDE_SENTINEL"));
    assert.ok(!call.json.includes('subagent_type'));
    assert.ok(call.json.includes("Memory active (1 insights"));
  }
  assert.equal(session.messages.filter(m => m.role === "custom" && m.customType === "mnemon").length, 1,
    "only the deliberately seeded old entry may remain; new guide messages must not persist");
  assert.equal(readFileSync(join(f.dataDir, "active"), "utf8"), f.activeBefore);
  assert.equal(f.run("status").total_insights, 1);
  assert.equal(f.run("status", "--store", "decoy").total_insights, 2);

  f.setPhase("compact");
  await session.compact("CALLER_COMPACTION_FOCUS");
  const compaction = calls.find(call => call.phase === "compact");
  assert.ok(compaction.json.includes("CALLER_COMPACTION_FOCUS"));
  assert.equal(compaction.guideCopies, 0);
  assert.deepEqual(compaction.tools, []);

  // Overflow recovery continues the agent without before_agent_start.
  f.setPhase("continuation");
  const starts = f.getStarts();
  session.agent.state.messages.push({ role: "user", content: "Offline continuation.", timestamp: Date.now() });
  await session.agent.continue();
  assert.equal(f.getStarts(), starts);
  assert.ok(calls.at(-1).json.includes("Context was compacted."));
  assert.ok(!session.messages.some(m => m.role === "custom" && m.customType === "mnemon-compaction"),
    "the post-compaction reminder must not persist in history");
  f.setPhase("next-turn");
  await session.prompt("Offline next user turn.");
  assert.ok(!calls.at(-1).json.includes("Context was compacted."));
  assert.equal(calls.at(-1).guideCopies, 1);
  t.diagnostic(JSON.stringify({ turns: 25, firstChars: calls[0].json.length, turn25Chars: calls[24].json.length,
    compactionChars: compaction.json.length, guideCopiesPerTurn: 1, providerNetworkCalls: 0 }));
});

test("a missing scoped Pi guide does not load shared host instructions", async (t) => {
  const f = await createFixture(t);
  rmSync(f.guidePath);
  await f.session.prompt("Offline prompt without installed Pi guide.");
  assert.ok(!f.calls[0].json.includes("CLAUDE_SHARED_GUIDE_SENTINEL"));
  assert.equal(f.calls[0].guideCopies, 0);
  assert.ok(f.calls[0].json.includes("complete and verify justified memory writes before the final answer"));
});
