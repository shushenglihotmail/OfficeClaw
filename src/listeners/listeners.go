// Package listeners provides message listening interfaces.
// The primary implementation is WhatsApp via the whatsapp package.
package listeners

import "context"

// IncomingMessage represents a trigger message from any source.
type IncomingMessage struct {
	Source   string // "whatsapp"
	ID       string // Message ID
	From     string // Sender identifier
	Body     string // Message body content
	ChatID   string // Chat/conversation ID for replies
	TaskName string // Parsed task name from trigger prefix
}

// MessageHandler is a callback invoked when a trigger message is received.
type MessageHandler func(ctx context.Context, msg IncomingMessage)
