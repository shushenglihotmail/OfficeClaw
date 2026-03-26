// Package listeners provides message listening interfaces.
// The primary implementation is Telegram via the telegram package.
package listeners

import "context"

// IncomingMessage represents a trigger message from any source.
type IncomingMessage struct {
	Source   string // "telegram"
	ID       string // Message ID
	From     string // Sender identifier
	Body     string // Message body content
	ChatID   string // Chat/conversation ID for replies
	TaskName string // Parsed task name from trigger prefix
}

// MessageHandler is a callback invoked when a trigger message is received.
type MessageHandler func(ctx context.Context, msg IncomingMessage)
