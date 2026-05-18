package assets

import "embed"

//go:embed claude/user_prompt.sh
var ClaudeUserPromptHook []byte

//go:embed claude/stop.sh
var ClaudeStopHook []byte

//go:embed claude/prime.sh
var ClaudePrimeHook []byte

//go:embed claude/compact.sh
var ClaudeCompactHook []byte

//go:embed claude/SKILL.md
var ClaudeSkill []byte

//go:embed claude/guide.md
var ClaudeGuide []byte

//go:embed codex/SKILL.md
var CodexSkill []byte

//go:embed codex/prime.sh
var CodexPrimeHook []byte

//go:embed codex/user_prompt.sh
var CodexUserPromptHook []byte

//go:embed codex/stop.sh
var CodexStopHook []byte

//go:embed openclaw/SKILL.md
var OpenClawSkill []byte

//go:embed openclaw/hooks/mnemon-prime/HOOK.md
var OpenClawHookMD []byte

//go:embed openclaw/hooks/mnemon-prime/handler.js
var OpenClawHookHandler []byte

//go:embed openclaw/plugin/package.json
var OpenClawPluginPackage []byte

//go:embed openclaw/plugin/openclaw.plugin.json
var OpenClawPluginManifest []byte

//go:embed openclaw/plugin/index.js
var OpenClawPluginIndex []byte

//go:embed nanoclaw/SKILL.md
var NanoClawSkill []byte

//go:embed nanoclaw/container-skill.md
var NanoClawContainerSkill []byte

//go:embed nanobot/SKILL.md
var NanobotSkill []byte

// All returns the embedded filesystem for inspection.
//
//go:embed claude codex openclaw nanoclaw nanobot
var All embed.FS
