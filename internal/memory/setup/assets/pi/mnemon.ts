import { readFileSync } from "node:fs";
import { join } from "node:path";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

function promptDir(): string {
  return join(process.env.MNEMON_DATA_DIR || join(process.env.HOME ?? "", ".mnemon"), "prompt", "pi");
}

function readGuide(): string {
  try {
    return readFileSync(join(promptDir(), "guide.md"), "utf8");
  } catch {
    // An explicit memory directory must not fall back to another host's guide.
    return "";
  }
}

async function memoryStatus(pi: ExtensionAPI): Promise<string> {
  try {
    const result = await pi.exec("mnemon", ["status"], { timeout: 5000 });
    if (result.code !== 0) throw new Error("mnemon status failed");
    const stats = JSON.parse(result.stdout);
    return `[mnemon] Memory active (${stats.total_insights ?? 0} insights, ${stats.edge_count ?? 0} edges).`;
  } catch {
    return "[mnemon] Memory status unavailable. Verify CLI results before claiming memory was read or saved.";
  }
}

export default function (pi: ExtensionAPI) {
  let recallAfterCompaction = false;

  pi.on("resources_discover", async () => {
    return {
      skillPaths: [join(process.env.PI_CODING_AGENT_DIR || join(process.env.HOME ?? "", ".pi", "agent"), "skills")],
    };
  });

  pi.on("session_start", async (_event, ctx) => {
    recallAfterCompaction = false;
    ctx.ui.setStatus("mnemon", "mnemon");
  });

  pi.on("before_agent_start", async (event) => {
    const guide = readGuide();
    const content = [await memoryStatus(pi), guide, "[mnemon] Recall when useful; complete and verify justified memory writes before the final answer. Preserve the selected memory store."]
      .filter(Boolean)
      .join("\n\n");

    // Pi resets this override from its base prompt on each user turn. A custom
    // message would instead persist another complete guide in the session.
    return { systemPrompt: [event.systemPrompt, content].filter(Boolean).join("\n\n") };
  });

  pi.on("context", async (event) => {
    // Old sessions can contain one guide message per turn from earlier versions.
    // Filter only our legacy message type, leaving other extensions' data intact.
    const messages = event.messages.filter((message) => !(message.role === "custom" && message.customType === "mnemon"));
    if (recallAfterCompaction) {
      recallAfterCompaction = false;
      messages.push({
        role: "custom",
        customType: "mnemon-compaction",
        content: "[mnemon] Context was compacted. Use focused recall to recheck relevant durable memory when details are missing. Do not store the summary as a new memory.",
        display: false,
        timestamp: Date.now(),
      });
    }
    return { messages };
  });

  pi.on("agent_end", async (_event, ctx) => {
    ctx.ui.notify("[mnemon] Consider whether this exchange warrants durable memory.", "info");
  });

  pi.on("session_compact", async () => {
    // The summarizer has no tools, and session_before_compact cannot return
    // customInstructions. Refresh memory on the next agent request, including
    // automatic overflow continuations which skip before_agent_start.
    recallAfterCompaction = true;
  });
}
