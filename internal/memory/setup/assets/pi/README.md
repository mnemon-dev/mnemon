# Pi memory extension regression

The opt-in test runs the production extension in the real Pi 0.83.0 SDK. Its
in-memory provider returns deterministic responses without making provider
requests. It uses temporary Pi configuration and Mnemon stores, with embeddings
limited to an unavailable localhost endpoint. It does not load user auth,
extensions, skills, or project context.

Use Node.js 22.19.0 or newer and install the fixed SDK in a disposable directory:

```sh
pi_memory_tools=$(mktemp -d)
npm install --prefix "$pi_memory_tools" --no-audit --no-fund @earendil-works/pi-coding-agent@0.83.0
go build -o mnemon .
PI_MEMORY_PACKAGE_DIR="$pi_memory_tools/node_modules/@earendil-works/pi-coding-agent" \
  node --test internal/memory/setup/assets/pi/mnemon.test.mjs
```

Run these commands from the repository root. `MNEMON_BIN` can select a different
built executable; `PI_MEMORY_TEST_TMPDIR` can select the parent directory for
temporary fixtures. The test rejects other SDK versions. Node may print a
module-type detection warning when loading the TypeScript extension; it does
not affect the test.

The assertions cover 25 turns without accumulating guide messages, preservation
of unrelated extension messages, removal of old Mnemon guide messages from
model requests, and a supported post-compaction reminder. The continuation check
calls the SDK agent without a new `before_agent_start`, as overflow recovery
does. It also verifies that inherited `MNEMON_STORE` wins over a different active
store, and that a missing Pi guide does not load shared host instructions.

This checks lifecycle and context behavior. It does not establish whether a
particular live model will follow the recall and write guidance.
