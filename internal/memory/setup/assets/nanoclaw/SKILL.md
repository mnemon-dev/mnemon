---
name: add-mnemon
description: Add persistent graph-based memory to NanoClaw agents using mnemon. Agents recall context before responding and remember insights after. Each group gets isolated memory with optional global shared store.
---

# /add-mnemon

Add [mnemon](https://github.com/mnemon-dev/mnemon) persistent memory to your NanoClaw installation. After running this skill, every agent session will have access to a per-group memory graph that persists across conversations.

## Architecture

```
Host                              Container
~/.mnemon/data/{group}/ ──rw──→ /home/node/.mnemon/data/default/  (private)
~/.mnemon/data/global/  ──ro──→ /home/node/.mnemon/data/global/   (shared)
```

Each group gets its own isolated mnemon store. An optional global store provides shared read-only knowledge across all groups.

---

## Phase 1: Pre-flight

1. Verify mnemon is installed on the host:
   ```bash
   mnemon --version
   ```
   If not installed:
   - **Recommended (npm)**: `npm install --global @mnemon-dev/mnemon`
   - **Alternative (Homebrew)**: `brew install --cask mnemon-dev/tap/mnemon`

2. Verify the container image exists:
   ```bash
   docker image inspect nanoclaw-agent:latest >/dev/null 2>&1 && echo "OK"
   ```

3. Fetch the latest mnemon version for the Dockerfile:
   ```bash
   curl -s https://api.github.com/repos/mnemon-dev/mnemon/releases/latest | grep -o '"tag_name": "v[^"]*"' | cut -d'"' -f4 | sed 's/^v//'
   ```

---

## Phase 2: Apply Code Changes

### 2a. Install mnemon in the container image

**File**: `container/Dockerfile`

Add the following block **after** the `apt-get install` section and **before** the `npm install -g` line. Replace `0.1.1` with the version from Phase 1 step 3:

```dockerfile
# Install mnemon for persistent agent memory
ARG MNEMON_VERSION=0.1.1
RUN ARCH=$(dpkg --print-architecture) && \
    curl -fsSL "https://github.com/mnemon-dev/mnemon/releases/download/v${MNEMON_VERSION}/mnemon_${MNEMON_VERSION}_linux_${ARCH}.tar.gz" \
    | tar -xz -C /usr/local/bin mnemon && \
    chmod +x /usr/local/bin/mnemon
```

This works for both `amd64` and `arm64` architectures.

### 2b. Add the container skill

**File**: `container/skills/mnemon/SKILL.md`

Create this file with the mnemon container skill content. This skill teaches the agent inside the container when and how to use mnemon. It should include:

- **Memory stores section**: Explain that the default store is per-group (private, read-write) and the global store is shared (read-only, accessed via `--store global --readonly`).
- **Recall guide**: Default recall on every new user message. Use `mnemon recall "<query>" --limit 5`. Also check the global store: `mnemon recall "<query>" --store global --readonly --limit 5`. Craft focused keyword-rich queries.
- **Remember guide**: Decision tree — Step 1: Does this exchange contain a user directive, reasoning conclusion, or durable observed state? Step 2: Does a memory already exist (create/update/skip)? Step 3: Is it worth storing?
- **Workflow**: remember → link (evaluate semantic/causal candidates with judgment) → recall.
- **Commands**: Full mnemon command reference (remember, link, recall, search, forget, related, gc, status, log).
- **Guardrails**: Never store secrets. Never write to the global store. Categories: preference, decision, insight, fact, context. Max 8,000 chars per insight.

### 2c. Add volume mounts for mnemon data

**File**: `src/container-runner.ts`

In the function that builds volume mounts (where the existing group folder and Claude session mounts are defined), add two new mounts **after** the Claude sessions mount:

```typescript
// Per-group mnemon memory store (private, read-write)
const groupMnemonDir = path.join(homedir(), '.mnemon', 'data', group.folder);
fs.mkdirSync(groupMnemonDir, { recursive: true });
mounts.push({
  hostPath: groupMnemonDir,
  containerPath: '/home/node/.mnemon/data/default',
  readonly: false,
});

// Global shared mnemon memory (read-only, optional)
const globalMnemonDir = path.join(homedir(), '.mnemon', 'data', 'global');
if (fs.existsSync(globalMnemonDir)) {
  mounts.push({
    hostPath: globalMnemonDir,
    containerPath: '/home/node/.mnemon/data/global',
    readonly: true,
  });
}
```

Adapt the mount syntax to match the existing pattern in `container-runner.ts` (it may use string format like `hostPath:containerPath:ro` or an object format — match whichever the file uses).

**Important**: The `mkdirSync` call ensures the per-group mnemon directory exists on the host before the container starts, preventing mount failures.

### 2d. Add lifecycle hook scripts

Create `container/hooks/mnemon/` with four shell scripts. These run inside the container at Claude Code lifecycle events to actively drive memory operations.

**File**: `container/hooks/mnemon/prime.sh`

```bash
#!/bin/bash
# mnemon SessionStart hook — report memory stats on session init.
STATS=$(mnemon status 2>/dev/null)
if [ -n "$STATS" ]; then
  INSIGHTS=$(echo "$STATS" | sed -n 's/.*"total_insights": *\([0-9]*\).*/\1/p' | head -1)
  EDGES=$(echo "$STATS" | sed -n 's/.*"edge_count": *\([0-9]*\).*/\1/p' | head -1)
  echo "[mnemon] Memory active (${INSIGHTS:-0} insights, ${EDGES:-0} edges)."
else
  echo "[mnemon] Memory active."
fi
```

**File**: `container/hooks/mnemon/user_prompt.sh`

```bash
#!/bin/bash
# mnemon UserPromptSubmit hook — remind agent to evaluate recall/remember.
echo "[mnemon] Evaluate: recall needed? After responding, evaluate: remember needed?"
```

**File**: `container/hooks/mnemon/stop.sh`

```bash
#!/bin/bash
# mnemon Stop hook — remind agent to consider remember after responding.
INPUT=$(cat)
MSG=$(echo "$INPUT" | jq -r '.last_assistant_message // ""' 2>/dev/null)
if echo "$MSG" | grep -qiE "mnemon remember|sub-agent.*remember|Stored.*imp="; then
  exit 0
fi
echo "[mnemon] Consider: does this exchange warrant a remember sub-agent?"
```

**File**: `container/hooks/mnemon/compact.sh`

```bash
#!/bin/bash
# mnemon PreCompact hook — save key insights before context compaction.
echo "[mnemon] Context compaction starting. Review this session and remember the most valuable insights (up to 5) before context is compressed. Delegate to Task sub-agents now."
```

Make all scripts executable: `chmod +x container/hooks/mnemon/*.sh`

### 2e. Copy hooks into container and register in settings.json

**File**: `container/Dockerfile`

Add after the mnemon binary install block:

```dockerfile
# Copy mnemon hook scripts
COPY hooks/mnemon/ /app/hooks/mnemon/
RUN chmod +x /app/hooks/mnemon/*.sh
```

**File**: `src/container-runner.ts`

In the block where `settings.json` is created for each group session (look for `writeFileSync` with `settings.json`), merge mnemon hooks into the settings object:

```typescript
// Register mnemon lifecycle hooks
const mnemonHooks = {
  SessionStart: [{
    hooks: [{ type: 'command', command: '/app/hooks/mnemon/prime.sh' }]
  }],
  UserPromptSubmit: [{
    hooks: [{ type: 'command', command: '/app/hooks/mnemon/user_prompt.sh' }]
  }],
  Stop: [{
    hooks: [{ type: 'command', command: '/app/hooks/mnemon/stop.sh' }]
  }],
  PreCompact: [{
    hooks: [{ type: 'command', command: '/app/hooks/mnemon/compact.sh' }]
  }],
};

// Merge into existing settings.hooks (preserve any existing hooks)
const existingHooks = settings.hooks || {};
for (const [event, entries] of Object.entries(mnemonHooks)) {
  existingHooks[event] = [...(existingHooks[event] || []), ...entries];
}
settings.hooks = existingHooks;
```

Adapt this to match the existing settings.json construction pattern in `container-runner.ts`.

---

## Phase 3: Setup

1. Initialize the global shared store on the host (optional — skip if you don't need cross-group shared memory):
   ```bash
   mnemon store create global
   ```

2. Rebuild the container image:
   ```bash
   ./container/build.sh
   ```

3. Restart the NanoClaw service:
   ```bash
   # macOS with launchd:
   launchctl kickstart -k "gui/$(id -u)/com.nanoclaw"
   # Or manually:
   npm run dev
   ```

---

## Phase 4: Verify

1. Verify mnemon works inside the container:
   ```bash
   docker run --rm --entrypoint mnemon nanoclaw-agent:latest --version
   ```

2. Verify mnemon status inside a running container:
   ```bash
   docker run --rm --entrypoint mnemon nanoclaw-agent:latest status
   ```

3. Send a test message to the WhatsApp bot and check that the agent mentions memory operations in its reasoning.

4. Verify data persistence on the host:
   ```bash
   ls ~/.mnemon/data/
   # Should show directories for each active group
   ```

---

## Removal

To remove mnemon from your NanoClaw installation:

1. Remove from Dockerfile: delete the `ARG MNEMON_VERSION` + `RUN ... mnemon` block and the `COPY hooks/mnemon/` line
2. Remove container skill: `rm -rf container/skills/mnemon/`
3. Remove hook scripts: `rm -rf container/hooks/mnemon/`
4. Remove volume mounts from `src/container-runner.ts`: delete the mnemon mount blocks
5. Remove hooks registration from `src/container-runner.ts`: delete the mnemon hooks merge in settings.json
6. Rebuild: `./container/build.sh`
7. (Optional) Remove data: `rm -rf ~/.mnemon/data/`
