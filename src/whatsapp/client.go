// Package whatsapp provides WhatsApp Web integration using whatsmeow.
package whatsapp

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/mdp/qrterminal/v3"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"

	_ "modernc.org/sqlite" // Pure Go SQLite driver (no CGO required)
	"google.golang.org/protobuf/proto"
)

// Client wraps the whatsmeow client for WhatsApp Web integration.
type Client struct {
	client        *whatsmeow.Client
	container     *sqlstore.Container
	logger        *log.Logger
	triggerPrefix string
	handler       MessageHandler
	mu            sync.RWMutex
}

// MessageHandler is called when a trigger message is received.
type MessageHandler func(ctx context.Context, msg IncomingMessage)

// IncomingMessage represents a WhatsApp message that triggered the agent.
type IncomingMessage struct {
	ID        string // Message ID
	ChatJID   string // Chat JID (sender or group)
	SenderJID string // Sender JID
	Body      string // Message body (without trigger prefix)
	IsGroup   bool   // Whether message is from a group
	TaskName  string // Parsed task name
}

// Config holds WhatsApp client configuration.
type Config struct {
	// Path to SQLite database for session storage
	DatabasePath string
	// Trigger prefix (e.g., "OfficeClaw:")
	TriggerPrefix string
	// Default task when none specified
	DefaultTask string
	// Logger
	Logger *log.Logger
}

// New creates a new WhatsApp client.
func New(cfg Config) (*Client, error) {
	if cfg.DatabasePath == "" {
		cfg.DatabasePath = "whatsapp.db"
	}
	if cfg.TriggerPrefix == "" {
		cfg.TriggerPrefix = "OfficeClaw:"
	}
	if cfg.Logger == nil {
		cfg.Logger = log.New(os.Stdout, "[whatsapp] ", log.LstdFlags)
	}

	// Create database container for session storage
	ctx := context.Background()
	dbLog := waLog.Noop
	// modernc.org/sqlite uses _pragma for foreign keys
	container, err := sqlstore.New(ctx, "sqlite", fmt.Sprintf("file:%s?_pragma=foreign_keys(1)", cfg.DatabasePath), dbLog)
	if err != nil {
		return nil, fmt.Errorf("failed to create session store: %w", err)
	}

	// Get or create device store
	deviceStore, err := container.GetFirstDevice(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get device store: %w", err)
	}

	// Create whatsmeow client
	clientLog := waLog.Noop
	client := whatsmeow.NewClient(deviceStore, clientLog)

	return &Client{
		client:        client,
		container:     container,
		logger:        cfg.Logger,
		triggerPrefix: cfg.TriggerPrefix,
	}, nil
}

// Connect connects to WhatsApp. If not logged in, displays QR code for scanning.
func (c *Client) Connect(ctx context.Context) error {
	// Register event handler
	c.client.AddEventHandler(c.eventHandler)

	if c.client.Store.ID == nil {
		// Not logged in - need to scan QR code
		qrChan, _ := c.client.GetQRChannel(ctx)
		err := c.client.Connect()
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}

		c.logger.Println("Scan the QR code with your WhatsApp app:")
		for evt := range qrChan {
			switch evt.Event {
			case "code":
				// Print QR code to terminal
				printQRCode(evt.Code)
				c.logger.Printf("QR Code: %s", evt.Code)
			case "success":
				c.logger.Println("QR code scanned successfully!")
				return nil
			case "timeout":
				return fmt.Errorf("QR code scan timed out")
			}
		}
	} else {
		// Already logged in - just connect
		err := c.client.Connect()
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		c.logger.Printf("Connected to WhatsApp as %s", c.client.Store.ID.User)
	}

	return nil
}

// SetMessageHandler sets the callback for incoming trigger messages.
func (c *Client) SetMessageHandler(handler MessageHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handler = handler
}

// eventHandler processes incoming WhatsApp events.
func (c *Client) eventHandler(evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:
		c.handleMessage(v)
	case *events.Connected:
		c.logger.Println("WhatsApp connected")
	case *events.Disconnected:
		c.logger.Println("WhatsApp disconnected")
	case *events.LoggedOut:
		c.logger.Println("WhatsApp logged out - please restart and scan QR code")
	}
}

// handleMessage processes incoming messages and triggers the agent if prefix matches.
func (c *Client) handleMessage(msg *events.Message) {
	// Get message text
	var text string
	if msg.Message.GetConversation() != "" {
		text = msg.Message.GetConversation()
	} else if msg.Message.GetExtendedTextMessage() != nil {
		text = msg.Message.GetExtendedTextMessage().GetText()
	} else {
		// Not a text message
		return
	}

	// Check if message starts with trigger prefix (case-insensitive)
	if !strings.HasPrefix(strings.ToLower(text), strings.ToLower(c.triggerPrefix)) {
		return
	}

	// Skip agent's own replies (messages from self that don't have trigger prefix were already filtered)
	// We allow trigger messages from self so you can control your own agent
	if msg.Info.IsFromMe {
		// This is a trigger message from self - that's fine, process it
		c.logger.Printf("Processing self-trigger message")
	}

	// Extract content after prefix (case-insensitive removal)
	content := text[len(c.triggerPrefix):]
	content = strings.TrimSpace(content)

	// Parse task name (first word) or use default
	taskName := "assist"
	parts := strings.Fields(content)
	if len(parts) > 0 {
		taskName = parts[0]
		content = strings.TrimSpace(strings.TrimPrefix(content, taskName))
	}

	c.logger.Printf("Trigger message from %s: task=%s", msg.Info.Sender.User, taskName)

	c.mu.RLock()
	handler := c.handler
	c.mu.RUnlock()

	if handler != nil {
		incoming := IncomingMessage{
			ID:        msg.Info.ID,
			ChatJID:   msg.Info.Chat.String(),
			SenderJID: msg.Info.Sender.String(),
			Body:      content,
			IsGroup:   msg.Info.IsGroup,
			TaskName:  taskName,
		}
		go handler(context.Background(), incoming)
	}
}

// SendMessage sends a text message to a chat.
func (c *Client) SendMessage(ctx context.Context, chatJID string, text string) error {
	jid, err := types.ParseJID(chatJID)
	if err != nil {
		return fmt.Errorf("invalid JID %s: %w", chatJID, err)
	}

	msg := &waE2E.Message{
		Conversation: proto.String(text),
	}

	_, err = c.client.SendMessage(ctx, jid, msg)
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	c.logger.Printf("Sent message to %s", chatJID)
	return nil
}

// Disconnect disconnects from WhatsApp.
func (c *Client) Disconnect() {
	c.client.Disconnect()
}

// IsConnected returns whether the client is connected.
func (c *Client) IsConnected() bool {
	return c.client.IsConnected()
}

// printQRCode prints a QR code to the terminal.
func printQRCode(code string) {
	fmt.Println()
	fmt.Println("Scan with WhatsApp → Linked Devices → Link a Device")
	fmt.Println()
	// Use HalfBlocks for a more compact QR code (half the height)
	qrterminal.GenerateHalfBlock(code, qrterminal.L, os.Stdout)
	fmt.Println()
}
