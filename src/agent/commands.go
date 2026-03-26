package agent

import (
	"fmt"
	"strings"
)

// Command represents a parsed slash command from a Telegram message.
type Command struct {
	Name string   // e.g., "reset", "model", "models", "clear", "summary", "help"
	Args string   // everything after the command name
}

// ParseCommand checks if a message body is a slash command.
// Returns nil if the message is not a command.
func ParseCommand(body string) *Command {
	body = strings.TrimSpace(body)
	if !strings.HasPrefix(body, "/") {
		return nil
	}

	// Split into command and args
	parts := strings.SplitN(body[1:], " ", 2)
	cmd := &Command{
		Name: strings.ToLower(parts[0]),
	}
	if len(parts) > 1 {
		cmd.Args = strings.TrimSpace(parts[1])
	}
	return cmd
}

// KnownModels tracks available models per agent mode.
// Models are populated from CLI output and can be extended.
type KnownModels struct {
	Claude  []string
	Copilot []string
}

// DefaultKnownModels returns the default known model lists.
// These are manually maintained since neither CLI exposes a --list-models flag.
// Update these when new models are added to the CLIs.
// Users can always use /model <any-name> to try unlisted models.
func DefaultKnownModels() KnownModels {
	return KnownModels{
		// Claude CLI models (from /model interactive menu)
		// Accepts aliases (sonnet, opus, haiku) or full names (claude-sonnet-4-6)
		Claude: []string{
			"sonnet",             // Sonnet 4.6 (default)
			"sonnet-1m",          // Sonnet 4.6 (1M context)
			"opus",               // Opus 4.6
			"opus-1m",            // Opus 4.6 (1M context)
			"haiku",              // Haiku 4.5
		},
		// Copilot CLI models (from /model interactive menu)
		Copilot: []string{
			"gpt-5.4",
			"gpt-5.3-codex",
			"gpt-5.2-codex",
			"gpt-5.2",
			"gpt-5.1-codex-max",
			"gpt-5.1-codex",
			"gpt-5.1",
			"gpt-5.4-mini",
			"gpt-5.1-codex-mini",
			"gpt-5-mini",
			"gpt-4.1",
			"claude-sonnet-4.6",
			"claude-sonnet-4.5",
			"claude-haiku-4.5",
			"claude-opus-4.6",
			"claude-opus-4.5",
			"claude-sonnet-4",
			"gemini-3-pro",
		},
	}
}

// validEffortLevels lists recognized reasoning effort levels for Copilot CLI.
var validEffortLevels = map[string]bool{
	"low": true, "medium": true, "high": true, "xhigh": true,
}

// parseModelArgs splits "/model gpt-5.4 high" into model name and optional effort level.
// If the last word is a valid effort level, it's extracted separately.
func parseModelArgs(args string) (model, effort string) {
	parts := strings.Fields(args)
	if len(parts) >= 2 {
		last := strings.ToLower(parts[len(parts)-1])
		if validEffortLevels[last] {
			return strings.Join(parts[:len(parts)-1], " "), last
		}
	}
	return args, ""
}

// FormatModelList returns a human-readable model list for a given mode.
// When currentModel is empty, the first model in the list is marked as default.
func FormatModelList(mode string, models []string, currentModel string) string {
	if len(models) == 0 {
		return fmt.Sprintf("No known models for %s mode. Use /model <name> to try any model.", mode)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Available %s models:\n", mode))
	matched := false
	for _, m := range models {
		if strings.EqualFold(m, currentModel) {
			sb.WriteString(fmt.Sprintf("  * %s (current)\n", m))
			matched = true
		} else {
			sb.WriteString(fmt.Sprintf("  - %s\n", m))
		}
	}
	// If current model didn't match any in the list but is set, show it separately
	if !matched && currentModel != "" {
		sb.WriteString(fmt.Sprintf("  * %s (current, custom)\n", currentModel))
	}
	// If no model is set at all, note the default
	if currentModel == "" {
		sb.WriteString(fmt.Sprintf("\nNo model override set — using %s default.\n", mode))
	}
	sb.WriteString("\nUse /model <name> to switch. Any model string accepted.")
	return sb.String()
}

// CommandHelpText returns help text listing all available commands for a given mode.
func CommandHelpText(mode string) string {
	var sb strings.Builder
	sb.WriteString("Available commands:\n")
	sb.WriteString("  /reset    - Clear session and start fresh\n")
	sb.WriteString("  /model <name> [effort] - Switch model (effort: low/medium/high/xhigh)\n")
	sb.WriteString("  /models   - List available models\n")

	switch mode {
	case "OC":
		sb.WriteString("  /clear    - Clear conversation context\n")
		sb.WriteString("  /summary  - Extract and save summary/facts\n")
	case "OCCO":
		sb.WriteString("  /effort <level> - Set reasoning effort (low/medium/high/xhigh)\n")
	case "OCC":
		// Claude CLI agent doesn't have /clear, /summary, or /effort
	}

	sb.WriteString("  /help     - Show this help\n")
	sb.WriteString("\nGlobal commands (all modes):\n")
	sb.WriteString("  /mute     - Mute this instance (only /unmute and /ping will work)\n")
	sb.WriteString("  /unmute   - Unmute this instance\n")
	sb.WriteString("  /ping     - Show machine name, state, and available modes\n")
	return sb.String()
}
