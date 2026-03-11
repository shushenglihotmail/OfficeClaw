package memory

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/officeclaw/src/llm"
)

// CheckFlushNeeded determines if the conversation context has reached the flush threshold.
// Returns true if context is at or above the threshold percentage, along with the usage ratio.
func CheckFlushNeeded(messages []llm.Message, maxTokens int, threshold float64) (bool, float64) {
	if maxTokens <= 0 {
		return false, 0
	}
	if threshold <= 0 {
		threshold = 0.8 // Default 80%
	}

	total := 0
	for _, msg := range messages {
		// Rough estimate: ~4 chars per token
		total += len(msg.Content) / 4

		// Also count tool call arguments
		for _, tc := range msg.ToolCalls {
			total += len(tc.Function.Arguments) / 4
		}
	}

	usage := float64(total) / float64(maxTokens)
	return usage >= threshold, usage
}

// GetDistillationPrompt returns the prompt to inject when distillation is triggered.
// usagePct is the current context usage (0.0-1.0). If 0, it's a manual trigger.
func GetDistillationPrompt(usagePct float64) string {
	var header string
	if usagePct > 0 {
		header = fmt.Sprintf("### MEMORY CHECKPOINT (%.0f%% context used)\n\n", usagePct*100)
	} else {
		header = "### MEMORY CHECKPOINT (manual summary requested)\n\n"
	}

	return header + `Before responding, extract important information from this session:

[SUMMARY]
(2-3 sentence recap of what was discussed/accomplished in this session)
[/SUMMARY]

[FACTS]
- Permanent rules or preferences discovered
- Important decisions to remember
- Technical details that will be true in future sessions
- (Leave empty if no durable facts to save)
[/FACTS]

After the markers, continue with your normal response to the user.`
}

var (
	summaryRegex = regexp.MustCompile(`(?is)\[SUMMARY\](.*?)\[/SUMMARY\]`)
	factsRegex   = regexp.MustCompile(`(?is)\[FACTS\](.*?)\[/FACTS\]`)
)

// ParseDistillationResponse extracts summary and facts from an LLM response containing markers.
func ParseDistillationResponse(response string) (summary, facts string) {
	// Extract summary
	if match := summaryRegex.FindStringSubmatch(response); len(match) > 1 {
		summary = strings.TrimSpace(match[1])
	}

	// Extract facts
	if match := factsRegex.FindStringSubmatch(response); len(match) > 1 {
		facts = strings.TrimSpace(match[1])
		// Filter out empty/placeholder responses
		lower := strings.ToLower(facts)
		if lower == "none" || lower == "n/a" || lower == "-" || lower == "" {
			facts = ""
		}
	}

	return summary, facts
}

// StripDistillationMarkers removes [SUMMARY] and [FACTS] markers from a response.
func StripDistillationMarkers(response string) string {
	// Remove [SUMMARY]...[/SUMMARY] blocks
	response = summaryRegex.ReplaceAllString(response, "")
	// Remove [FACTS]...[/FACTS] blocks
	response = factsRegex.ReplaceAllString(response, "")

	// Clean up extra whitespace
	response = strings.TrimSpace(response)

	// Remove leading "---" or "###" if that's all that remains at the start
	lines := strings.Split(response, "\n")
	for len(lines) > 0 && (strings.TrimSpace(lines[0]) == "---" || strings.HasPrefix(strings.TrimSpace(lines[0]), "### MEMORY")) {
		lines = lines[1:]
	}

	return strings.TrimSpace(strings.Join(lines, "\n"))
}
