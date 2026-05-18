# Mnemon Harness Setup

This directory contains the shared setup entrypoints for projecting canonical
Mnemon harness modules into host runtimes.

```text
harness/setup/
├── install.sh
├── status.sh
├── uninstall.sh
├── lib/
└── schema/
```

Use the shared entrypoints for new integrations:

```bash
bash harness/setup/install.sh --host claude-code --module memory-loop
bash harness/setup/status.sh --host claude-code
bash harness/setup/uninstall.sh --host claude-code --module memory-loop
bash harness/setup/install.sh --host codex --module memory-loop
bash harness/setup/install.sh --host codex --module eval-loop
```

Host-specific projection logic lives under `harness/hosts/<host>/`. Loop assets
live under `harness/modules/<module>/`.
