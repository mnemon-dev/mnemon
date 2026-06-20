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

//go:embed cursor/SKILL.md
var CursorSkill []byte

//go:embed cursor/prime.sh
var CursorPrimeHook []byte

//go:embed cursor/stop.sh
var CursorStopHook []byte

//go:embed cursor/compact.sh
var CursorCompactHook []byte

//go:embed trae/SKILL.md
var TraeSkill []byte

//go:embed trae/prime.sh
var TraePrimeHook []byte

//go:embed trae/user_prompt.sh
var TraeUserPromptHook []byte

//go:embed trae/stop.sh
var TraeStopHook []byte

//go:embed qoder/SKILL.md
var QoderSkill []byte

//go:embed qoderwork/SKILL.md
var QoderWorkSkill []byte

//go:embed qoder/prime.sh
var QoderPrimeHook []byte

//go:embed qoder/user_prompt.sh
var QoderUserPromptHook []byte

//go:embed qoder/stop.sh
var QoderStopHook []byte

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

//go:embed pi/SKILL.md
var PiSkill []byte

//go:embed pi/mnemon.ts
var PiExtension []byte

//go:embed hermes/SKILL.md
var HermesSkill []byte

//go:embed hermes/prime.sh
var HermesPrimeHook []byte

//go:embed hermes/remind.sh
var HermesRemindHook []byte

//go:embed hermes/nudge.sh
var HermesNudgeHook []byte

//go:embed hermes/compact.sh
var HermesCompactHook []byte

// All returns the embedded filesystem for inspection.
//
//go:embed claude codex cursor trae qoder qoderwork openclaw nanoclaw nanobot pi hermes
var All embed.FS
