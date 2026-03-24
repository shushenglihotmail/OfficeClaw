// Package pending implements a persistent message queue for unsent WhatsApp replies.
// When OfficeClaw shuts down with in-flight messages, unsent replies are saved to disk
// and retried on the next startup.
package pending

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"sync"
	"time"
)

// Message represents an unsent WhatsApp reply.
type Message struct {
	ChatJID   string    `json:"chat_jid"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
	Attempts  int       `json:"attempts"`
}

// Sender is the interface for sending WhatsApp messages.
type Sender interface {
	SendMessage(ctx context.Context, chatJID string, text string) error
	IsConnected() bool
}

// Queue manages unsent messages with disk persistence.
type Queue struct {
	filePath string
	messages []Message
	mu       sync.Mutex
	logger   *log.Logger
}

// NewQueue creates a queue backed by the given file path.
func NewQueue(filePath string, logger *log.Logger) *Queue {
	q := &Queue{
		filePath: filePath,
		logger:   logger,
	}
	q.load()
	return q
}

// Add enqueues a message for later delivery.
func (q *Queue) Add(chatJID, text string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.messages = append(q.messages, Message{
		ChatJID:   chatJID,
		Text:      text,
		CreatedAt: time.Now(),
	})
	q.save()
	q.logger.Printf("[pending] Queued message for %s (%d chars)", chatJID, len(text))
}

// Len returns the number of pending messages.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.messages)
}

// Drain attempts to send all pending messages. Successfully sent messages are removed.
// Messages older than maxAge are discarded.
func (q *Queue) Drain(ctx context.Context, sender Sender, maxAge time.Duration) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.messages) == 0 {
		return
	}

	q.logger.Printf("[pending] Draining %d pending messages", len(q.messages))

	var remaining []Message
	now := time.Now()

	for _, msg := range q.messages {
		// Discard messages older than maxAge
		if now.Sub(msg.CreatedAt) > maxAge {
			q.logger.Printf("[pending] Discarding expired message for %s (age: %v)", msg.ChatJID, now.Sub(msg.CreatedAt))
			continue
		}

		// Try to send
		if err := sender.SendMessage(ctx, msg.ChatJID, msg.Text); err != nil {
			msg.Attempts++
			q.logger.Printf("[pending] Failed to send to %s (attempt %d): %v", msg.ChatJID, msg.Attempts, err)

			// Keep for retry if under max attempts
			if msg.Attempts < 5 {
				remaining = append(remaining, msg)
			} else {
				q.logger.Printf("[pending] Giving up on message for %s after %d attempts", msg.ChatJID, msg.Attempts)
			}
		} else {
			q.logger.Printf("[pending] Successfully sent pending message to %s", msg.ChatJID)
		}
	}

	q.messages = remaining
	q.save()
}

// load reads pending messages from disk.
func (q *Queue) load() {
	data, err := os.ReadFile(q.filePath)
	if err != nil {
		if !os.IsNotExist(err) {
			q.logger.Printf("[pending] Error reading queue file: %v", err)
		}
		return
	}

	if err := json.Unmarshal(data, &q.messages); err != nil {
		q.logger.Printf("[pending] Error parsing queue file: %v", err)
		q.messages = nil
		return
	}

	if len(q.messages) > 0 {
		q.logger.Printf("[pending] Loaded %d pending messages from disk", len(q.messages))
	}
}

// save writes pending messages to disk.
func (q *Queue) save() {
	if len(q.messages) == 0 {
		// Remove file if no pending messages
		os.Remove(q.filePath)
		return
	}

	data, err := json.MarshalIndent(q.messages, "", "  ")
	if err != nil {
		q.logger.Printf("[pending] Error marshaling queue: %v", err)
		return
	}

	if err := os.WriteFile(q.filePath, data, 0644); err != nil {
		q.logger.Printf("[pending] Error writing queue file: %v", err)
	}
}
