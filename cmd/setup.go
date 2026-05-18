package cmd

import (
	"fmt"
	"strings"

	"github.com/mnemon-dev/mnemon/internal/setup"
	"github.com/mnemon-dev/mnemon/internal/setup/assets"
	"github.com/mnemon-dev/mnemon/internal/store"
	"github.com/spf13/cobra"
)

var (
	setupTarget string
	setupEject  bool
	setupYes    bool
	setupGlobal bool
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Deploy mnemon into LLM CLI environments",
	Long: `Detect installed LLM CLIs and deploy mnemon integration.

By default, installs to project-local config (.claude/, .codex/, .openclaw/, .nanobot/).
Use --global to install to user-wide config (~/.claude/, ~/.codex/, ~/.openclaw/, ~/.nanobot/workspace/).

Supported environments: Claude Code, Codex, OpenClaw, Nanobot.

Examples:
  mnemon setup                              # Interactive: project-local install
  mnemon setup --global                     # Interactive: user-wide install
  mnemon setup --target claude-code         # Non-interactive: Claude Code only
  mnemon setup --eject                      # Interactive: remove integrations
  mnemon setup --eject --target claude-code # Non-interactive: remove Claude Code only
  mnemon setup --yes                        # Auto-confirm all prompts`,
	RunE: runSetup,
}

func init() {
	setupCmd.Flags().StringVar(&setupTarget, "target", "", "target environment (claude-code, codex, openclaw, nanobot)")
	setupCmd.Flags().BoolVar(&setupEject, "eject", false, "remove mnemon integrations")
	setupCmd.Flags().BoolVar(&setupYes, "yes", false, "auto-confirm all prompts (CI-friendly)")
	setupCmd.Flags().BoolVar(&setupGlobal, "global", false, "install to user-wide config instead of project-local config")
	rootCmd.AddCommand(setupCmd)
}

func runSetup(cmd *cobra.Command, args []string) error {
	if setupTarget != "" && setupTarget != "claude-code" && setupTarget != "codex" && setupTarget != "openclaw" && setupTarget != "nanobot" {
		return fmt.Errorf("invalid target %q (must be claude-code, codex, openclaw, or nanobot)", setupTarget)
	}

	envs := setup.DetectEnvironments(setupGlobal)

	if setupEject {
		return runEjectFlow(envs)
	}
	return runInstallFlow(envs)
}

func runInstallFlow(envs []setup.Environment) error {
	// Non-interactive with --target: install specific target directly
	if setupTarget != "" {
		for i := range envs {
			if envs[i].Name == setupTarget {
				return installEnv(&envs[i])
			}
		}
		return fmt.Errorf("unknown target %q", setupTarget)
	}

	// Detection display
	fmt.Println("Detecting LLM CLI environments...")
	fmt.Println()

	var detected []setup.Environment
	for _, env := range envs {
		setup.DetectionLine(env.Detected, env.Display, env.Version, env.ConfigDir)
		if env.Detected {
			detected = append(detected, env)
		}
	}

	if len(detected) == 0 {
		fmt.Println("\nNo supported LLM CLI environments detected.")
		fmt.Println("Install Claude Code, Codex, OpenClaw, or Nanobot, then run 'mnemon setup' again.")
		return nil
	}

	// Select environment
	var selected []setup.Environment
	if setupYes {
		selected = detected
	} else if setup.IsInteractive() {
		options := make([]string, len(detected))
		for i, env := range detected {
			options[i] = env.Display
		}
		idx := setup.SelectOne("Select environment", options, 0)
		selected = []setup.Environment{detected[idx]}
	} else {
		selected = detected
	}

	if len(selected) == 0 {
		fmt.Println("\nNo environments selected.")
		return nil
	}

	var errCount int
	for i := range selected {
		if err := installEnv(&selected[i]); err != nil {
			errCount++
		}
	}

	if errCount > 0 {
		return fmt.Errorf("%d error(s) during setup", errCount)
	}
	return nil
}

func installEnv(env *setup.Environment) error {
	var err error
	switch env.Name {
	case "claude-code":
		err = installClaudeCode(env)
	case "codex":
		err = installCodex(env)
	case "openclaw":
		err = installOpenClaw(env)
	case "nanobot":
		err = installNanobot(env)
	}
	if err != nil {
		return err
	}
	return initDefaultStore()
}

// initDefaultStore migrates a legacy DB if present, then ensures the
// default store exists so the data directory is ready to use.
func initDefaultStore() error {
	if err := store.MigrateIfNeeded(dataDir); err != nil {
		fmt.Printf("  Warning: migration failed: %v\n", err)
	}
	if !store.StoreExists(dataDir, store.DefaultStoreName) {
		dir := store.StoreDir(dataDir, store.DefaultStoreName)
		db, err := store.Open(dir)
		if err != nil {
			return fmt.Errorf("init default store: %w", err)
		}
		db.Close()
		fmt.Printf("  Initialized default store at %s\n", dir)
	}
	return nil
}

// ─── Claude Code ────────────────────────────────────────────────────

func installClaudeCode(env *setup.Environment) error {
	configDir := env.ConfigDir

	// Scope selection (only when interactive and --global not explicitly set)
	if !setupGlobal && !setupYes && setup.IsInteractive() {
		home := setup.HomeDir()
		localDir := ".claude"
		globalDir := home + "/.claude"
		idx := setup.SelectOne("Install scope",
			[]string{
				fmt.Sprintf("Local — this project only (%s/)", localDir),
				fmt.Sprintf("Global — all projects (%s/)", globalDir),
			}, 0)
		if idx == 1 {
			configDir = globalDir
		} else {
			configDir = localDir
		}
	}

	fmt.Printf("\nSetting up Claude Code (%s)...\n", configDir)

	// Phase 1: Skill
	fmt.Println("\n[1/3] Skill")
	if path, err := setup.ClaudeWriteSkill(configDir); err != nil {
		setup.StatusError(0, 0, "Skill", err)
		return err
	} else {
		setup.StatusOK(0, 0, "Skill", path)
	}

	// Phase 2: Prompt files (guide.md + skill.md → ~/.mnemon/prompt/)
	fmt.Println("\n[2/3] Prompts")
	if path, err := setup.WritePromptFiles(); err != nil {
		setup.StatusError(0, 0, "Prompts", err)
		return err
	} else {
		setup.StatusOK(0, 0, "Prompts", path)
	}

	if path, err := setup.ClaudeWriteHook(configDir, "prime.sh", assets.ClaudePrimeHook); err != nil {
		setup.StatusError(0, 0, "Hook: prime", err)
		return err
	} else {
		setup.StatusOK(0, 0, "Hook: prime", path)
	}

	// Phase 3: Optional hooks
	fmt.Println("\n[3/3] Optional hooks")
	sel := selectOptionalHooks()

	if sel.Remind {
		if path, err := setup.ClaudeWriteHook(configDir, "user_prompt.sh", assets.ClaudeUserPromptHook); err != nil {
			setup.StatusError(0, 0, "Hook: remind", err)
		} else {
			setup.StatusOK(0, 0, "Hook: remind", path)
		}
	}
	if sel.Nudge {
		if path, err := setup.ClaudeWriteHook(configDir, "stop.sh", assets.ClaudeStopHook); err != nil {
			setup.StatusError(0, 0, "Hook: nudge", err)
		} else {
			setup.StatusOK(0, 0, "Hook: nudge", path)
		}
	}
	if sel.Compact {
		if path, err := setup.ClaudeWriteHook(configDir, "compact.sh", assets.ClaudeCompactHook); err != nil {
			setup.StatusError(0, 0, "Hook: compact", err)
		} else {
			setup.StatusOK(0, 0, "Hook: compact", path)
		}
	}

	// Register hooks
	if path, err := setup.ClaudeRegisterHooks(configDir, sel); err != nil {
		setup.StatusError(0, 0, "Settings", err)
	} else {
		setup.StatusUpdated(0, 0, "Settings", path)
	}

	// Summary
	var hookNames []string
	hookNames = append(hookNames, "prime")
	if sel.Remind {
		hookNames = append(hookNames, "remind")
	}
	if sel.Nudge {
		hookNames = append(hookNames, "nudge")
	}
	if sel.Compact {
		hookNames = append(hookNames, "compact")
	}
	fmt.Println()
	fmt.Println("Setup complete!")
	fmt.Printf("  Hooks   %s\n", strings.Join(hookNames, ", "))
	fmt.Printf("  Prompts ~/.mnemon/prompt/ (guide.md, skill.md)\n")
	fmt.Println()
	fmt.Println("Start a new Claude Code session to activate.")
	fmt.Println("Edit ~/.mnemon/prompt/guide.md to customize behavior.")
	fmt.Println("Run 'mnemon setup --eject' to remove.")

	return nil
}

// selectOptionalHooks prompts user for which optional hooks to enable.
func selectOptionalHooks() setup.HookSelection {
	sel := setup.HookSelection{Remind: true, Nudge: true, Compact: false}

	if setupYes || !setup.IsInteractive() {
		return sel
	}

	opts := []string{
		"Remind  — remind agent to recall & remember on each message (recommended)",
		"Nudge   — remind about memory on session end",
		"Compact — save key insights before context compaction",
	}
	defs := []bool{true, true, false}
	choices := setup.SelectMulti("Select hooks to enable", opts, defs)

	sel.Remind = choices[0]
	sel.Nudge = choices[1]
	sel.Compact = choices[2]
	return sel
}

// ─── Codex ──────────────────────────────────────────────────────────

func installCodex(env *setup.Environment) error {
	configDir := env.ConfigDir

	if !setupGlobal && !setupYes && setup.IsInteractive() {
		home := setup.HomeDir()
		localDir := ".codex"
		globalDir := home + "/.codex"
		idx := setup.SelectOne("Install scope",
			[]string{
				fmt.Sprintf("Local — this project only (%s/)", localDir),
				fmt.Sprintf("Global — all projects (%s/)", globalDir),
			}, 0)
		if idx == 1 {
			configDir = globalDir
		} else {
			configDir = localDir
		}
	}

	fmt.Printf("\nSetting up Codex (%s)...\n", configDir)

	fmt.Println("\n[1/4] Skill")
	if path, err := setup.CodexWriteSkill(configDir); err != nil {
		setup.StatusError(0, 0, "Skill", err)
		return err
	} else {
		setup.StatusOK(0, 0, "Skill", path)
	}

	fmt.Println("\n[2/4] Prompts")
	if path, err := setup.WritePromptFiles(); err != nil {
		setup.StatusError(0, 0, "Prompts", err)
		return err
	} else {
		setup.StatusOK(0, 0, "Prompts", path)
	}

	fmt.Println("\n[3/4] Hooks")
	if path, err := setup.CodexWriteHook(configDir, "prime.sh", assets.CodexPrimeHook); err != nil {
		setup.StatusError(0, 0, "Hook: prime", err)
		return err
	} else {
		setup.StatusOK(0, 0, "Hook: prime", path)
	}
	if path, err := setup.CodexWriteHook(configDir, "user_prompt.sh", assets.CodexUserPromptHook); err != nil {
		setup.StatusError(0, 0, "Hook: remind", err)
		return err
	} else {
		setup.StatusOK(0, 0, "Hook: remind", path)
	}
	if path, err := setup.CodexWriteHook(configDir, "stop.sh", assets.CodexStopHook); err != nil {
		setup.StatusError(0, 0, "Hook: stop", err)
		return err
	} else {
		setup.StatusOK(0, 0, "Hook: stop", path)
	}

	fmt.Println("\n[4/4] Config")
	if path, err := setup.CodexRegisterHooks(configDir); err != nil {
		setup.StatusError(0, 0, "Hooks config", err)
		return err
	} else {
		setup.StatusUpdated(0, 0, "Hooks config", path)
	}

	fmt.Println()
	fmt.Println("Setup complete!")
	fmt.Printf("  Skill   %s/skills/mnemon/SKILL.md\n", configDir)
	fmt.Printf("  Hooks   %s/hooks.json (SessionStart, UserPromptSubmit, Stop)\n", configDir)
	fmt.Printf("  Prompts ~/.mnemon/prompt/ (guide.md, skill.md)\n")
	fmt.Println()
	fmt.Println("Start a new Codex session to activate.")
	fmt.Println("Run 'mnemon setup --eject --target codex' to remove.")

	return nil
}

// ─── OpenClaw ───────────────────────────────────────────────────────

func installOpenClaw(env *setup.Environment) error {
	configDir := env.ConfigDir

	// Scope selection: OpenClaw defaults to global (~/.openclaw/) because
	// plugin/hook discovery only reads ~/.openclaw/ by default.
	if !setupGlobal && !setupYes && setup.IsInteractive() {
		home := setup.HomeDir()
		localDir := ".openclaw"
		globalDir := home + "/.openclaw"
		idx := setup.SelectOne("Install scope",
			[]string{
				fmt.Sprintf("Global — all projects (%s/)", globalDir),
				fmt.Sprintf("Local  — this project only (%s/)", localDir),
			}, 0) // default: Global
		if idx == 1 {
			configDir = localDir
		} else {
			configDir = globalDir
		}
	}

	fmt.Printf("\nSetting up OpenClaw (%s)...\n", configDir)

	// Phase 1: Skill
	fmt.Println("\n[1/4] Skill")
	if path, err := setup.OpenClawWriteSkill(configDir); err != nil {
		setup.StatusError(0, 0, "Skill", err)
		return err
	} else {
		setup.StatusOK(0, 0, "Skill", path)
	}

	// Phase 2: Prompt files (guide.md + skill.md → ~/.mnemon/prompt/)
	fmt.Println("\n[2/4] Prompts")
	if path, err := setup.WritePromptFiles(); err != nil {
		setup.StatusError(0, 0, "Prompts", err)
		return err
	} else {
		setup.StatusOK(0, 0, "Prompts", path)
	}

	// Phase 3: Internal hook (agent:bootstrap → inject guide)
	fmt.Println("\n[3/4] Hook")
	if path, err := setup.OpenClawWriteHook(configDir); err != nil {
		setup.StatusError(0, 0, "Hook: prime", err)
	} else {
		setup.StatusOK(0, 0, "Hook: prime", path)
	}

	// Phase 4: Plugin (optional hooks selection + install)
	fmt.Println("\n[4/4] Plugin")
	sel := selectOpenClawOptionalHooks()

	if path, err := setup.OpenClawWritePlugin(configDir, version); err != nil {
		setup.StatusError(0, 0, "Plugin", err)
	} else {
		setup.StatusOK(0, 0, "Plugin", path)
	}

	if path, err := setup.OpenClawRegisterPlugin(configDir, sel); err != nil {
		setup.StatusError(0, 0, "Config", err)
	} else {
		setup.StatusUpdated(0, 0, "Config", path)
	}

	// Summary
	var hookNames []string
	hookNames = append(hookNames, "prime")
	if sel.Remind {
		hookNames = append(hookNames, "remind")
	}
	if sel.Nudge {
		hookNames = append(hookNames, "nudge")
	}
	if sel.Compact {
		hookNames = append(hookNames, "compact")
	}

	fmt.Println()
	fmt.Println("Setup complete!")
	fmt.Printf("  Skill   %s/skills/mnemon/SKILL.md\n", configDir)
	fmt.Printf("  Hook    %s/hooks/mnemon-prime/ (agent:bootstrap)\n", configDir)
	fmt.Printf("  Plugin  %s/extensions/mnemon/ (hooks: %s)\n", configDir, strings.Join(hookNames, ", "))
	fmt.Printf("  Prompts ~/.mnemon/prompt/ (guide.md, skill.md)\n")
	fmt.Println()
	fmt.Println("Restart the OpenClaw gateway to activate.")
	fmt.Println("Edit ~/.mnemon/prompt/guide.md to customize behavior.")
	fmt.Println("Run 'mnemon setup --eject' to remove.")

	return nil
}

// selectOpenClawOptionalHooks prompts user for which plugin hooks to enable.
// Remind and Nudge default on; Compact defaults off — mirrors Claude Code behaviour.
func selectOpenClawOptionalHooks() setup.HookSelection {
	sel := setup.HookSelection{Remind: true, Nudge: true, Compact: false}

	if setupYes || !setup.IsInteractive() {
		return sel
	}

	opts := []string{
		"Remind  — recall relevant memories + remind agent on each message (recommended)",
		"Nudge   — suggest remember sub-agent after each reply",
		"Compact — save key insights before context compaction",
	}
	defs := []bool{true, true, false}
	choices := setup.SelectMulti("Select plugin hooks to enable", opts, defs)

	sel.Remind = choices[0]
	sel.Nudge = choices[1]
	sel.Compact = choices[2]
	return sel
}

// ─── Nanobot ────────────────────────────────────────────────────────

func installNanobot(env *setup.Environment) error {
	configDir := env.ConfigDir

	// Scope selection
	if !setupGlobal && !setupYes && setup.IsInteractive() {
		home := setup.HomeDir()
		localDir := ".nanobot"
		globalDir := home + "/.nanobot/workspace"
		idx := setup.SelectOne("Install scope",
			[]string{
				fmt.Sprintf("Global — all projects (%s/)", globalDir),
				fmt.Sprintf("Local  — this project only (%s/)", localDir),
			}, 0) // default: Global
		if idx == 1 {
			configDir = localDir
		} else {
			configDir = globalDir
		}
	}

	fmt.Printf("\nSetting up Nanobot (%s)...\n", configDir)

	// Phase 1: Skill
	fmt.Println("\n[1/2] Skill")
	if path, err := setup.NanobotWriteSkill(configDir); err != nil {
		setup.StatusError(0, 0, "Skill", err)
		return err
	} else {
		setup.StatusOK(0, 0, "Skill", path)
	}

	// Phase 2: Prompt files (guide.md + skill.md → ~/.mnemon/prompt/)
	fmt.Println("\n[2/2] Prompts")
	if path, err := setup.WritePromptFiles(); err != nil {
		setup.StatusError(0, 0, "Prompts", err)
		return err
	} else {
		setup.StatusOK(0, 0, "Prompts", path)
	}

	// Summary
	fmt.Println()
	fmt.Println("Setup complete!")
	fmt.Printf("  Skill   %s/skills/mnemon/SKILL.md\n", configDir)
	fmt.Printf("  Prompts ~/.mnemon/prompt/ (guide.md, skill.md)\n")
	fmt.Println()
	fmt.Println("Restart Nanobot to activate the mnemon skill.")
	fmt.Println("Edit ~/.mnemon/prompt/guide.md to customize behavior.")
	fmt.Println("Run 'mnemon setup --eject' to remove.")

	return nil
}

// ─── Eject ──────────────────────────────────────────────────────────

func runEjectFlow(envs []setup.Environment) error {
	if setupTarget != "" {
		for i := range envs {
			if envs[i].Name == setupTarget {
				return ejectEnv(&envs[i])
			}
		}
		return fmt.Errorf("unknown target %q", setupTarget)
	}

	fmt.Println("Detecting LLM CLI environments...")
	fmt.Println()

	var installed []setup.Environment
	for _, env := range envs {
		setup.DetectionLine(env.Detected, env.Display, env.Version, env.ConfigDir)
		if env.Detected {
			installed = append(installed, env)
		}
	}

	if len(installed) == 0 {
		fmt.Println("\nNo environments detected.")
		return nil
	}

	var selected []setup.Environment
	if setupYes {
		selected = installed
	} else if setup.IsInteractive() {
		options := make([]string, len(installed))
		for i, env := range installed {
			options[i] = env.Display
		}
		idx := setup.SelectOne("Select environment to remove", options, 0)
		selected = []setup.Environment{installed[idx]}
	} else {
		selected = installed
	}

	if len(selected) == 0 {
		fmt.Println("\nNo environments selected.")
		return nil
	}

	var errCount int
	for i := range selected {
		if err := ejectEnv(&selected[i]); err != nil {
			errCount++
		}
	}

	fmt.Println()
	fmt.Println("Done! All selected integrations removed.")

	if errCount > 0 {
		return fmt.Errorf("%d error(s) during eject", errCount)
	}
	return nil
}

func ejectEnv(env *setup.Environment) error {
	switch env.Name {
	case "claude-code":
		errs := setup.ClaudeEject(env.ConfigDir)
		ejectMarkdown("CLAUDE.md", "Remove memory guidance from ./CLAUDE.md?")
		if len(errs) > 0 {
			return errs[0]
		}

	case "codex":
		errs := setup.CodexEject(env.ConfigDir)
		ejectMarkdown("AGENTS.md", "Remove memory guidance from ./AGENTS.md?")
		if len(errs) > 0 {
			return errs[0]
		}

	case "openclaw":
		errs := setup.OpenClawEject(env.ConfigDir)
		ejectMarkdown("AGENTS.md", "Remove memory guidance from ./AGENTS.md?")
		if len(errs) > 0 {
			return errs[0]
		}

	case "nanobot":
		errs := setup.NanobotEject(env.ConfigDir)
		ejectMarkdown("AGENTS.md", "Remove memory guidance from ./AGENTS.md?")
		if len(errs) > 0 {
			return errs[0]
		}
	}
	return nil
}

func ejectMarkdown(filePath string, prompt string) {
	if setupYes {
		if changed, err := setup.EjectMemoryBlock(filePath); err != nil {
			fmt.Printf("  Warning: could not clean %s: %v\n", filePath, err)
		} else if changed {
			fmt.Printf("  Memory guidance removed from %s\n", filePath)
		}
	} else if setup.IsInteractive() {
		if setup.Confirm(prompt, true) {
			if changed, err := setup.EjectMemoryBlock(filePath); err != nil {
				fmt.Printf("  Warning: could not clean %s: %v\n", filePath, err)
			} else if changed {
				fmt.Printf("  Memory guidance removed from %s\n", filePath)
			}
		}
	}
}
