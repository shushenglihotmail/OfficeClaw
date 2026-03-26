package tools

import (
	"context"
	"fmt"
	"log"

	"github.com/officeclaw/src/telegram"
)

// MessagingTool allows the LLM to send Telegram messages.
type MessagingTool struct {
	client *telegram.Client
}

// NewMessagingTool creates a messaging tool with a Telegram client.
func NewMessagingTool(client *telegram.Client) *MessagingTool {
	return &MessagingTool{client: client}
}

func (t *MessagingTool) Name() string { return "send_message" }

func (t *MessagingTool) Description() string {
	return "Send a Telegram message reply. Use this to respond to the user's Telegram message."
}

func (t *MessagingTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"chat_id": map[string]interface{}{
				"type":        "string",
				"description": "Telegram chat ID to send the message to",
			},
			"message": map[string]interface{}{
				"type":        "string",
				"description": "The text message content to send",
			},
		},
		"required": []string{"chat_id", "message"},
	}
}

type messagingArgs struct {
	ChatID  string `json:"chat_id"`
	Message string `json:"message"`
}

func (t *MessagingTool) Execute(ctx context.Context, arguments string) (string, error) {
	args, err := ParseArgs[messagingArgs](arguments)
	if err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Debug: log what we're about to send
	log.Printf("[messaging] Sending to %s, message length: %d", args.ChatID, len(args.Message))

	if args.Message == "" {
		return "", fmt.Errorf("message content is empty")
	}

	err = t.client.SendMessage(ctx, args.ChatID, args.Message)
	if err != nil {
		return "", fmt.Errorf("failed to send message: %w", err)
	}

	return fmt.Sprintf("Message sent to %s", args.ChatID), nil
}
