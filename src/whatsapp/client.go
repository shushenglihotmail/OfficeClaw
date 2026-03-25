// Package whatsapp provides WhatsApp Web integration using whatsmeow.
package whatsapp

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"

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

// MessageMode indicates which agent mode to use for a message.
type MessageMode string

const (
	ModeOfficeClaw MessageMode = "officeclaw" // Custom OfficeClaw agent
	ModeClaude     MessageMode = "claude"     // Direct Claude CLI agent
	ModeCopilot    MessageMode = "copilot"    // Direct Copilot CLI agent
)

// Client wraps the whatsmeow client for WhatsApp Web integration.
type Client struct {
	client             *whatsmeow.Client
	container          *sqlstore.Container
	logger             *log.Logger
	triggerPrefix      string       // e.g., "OC:"
	claudeTrigger      string       // e.g., "OCC:"
	copilotTrigger     string       // hardcoded "OCCO:"
	machineName        string       // short hostname, resolved at startup (for targeted messaging)
	handler            MessageHandler
	claudeHandler      MessageHandler
	copilotHandler     MessageHandler
	mu                 sync.RWMutex
	wg                 sync.WaitGroup // tracks in-flight message handlers
	shutdownCh         chan struct{}   // closed when shutdown starts

	// Reconnection state
	connected     bool
	connMu        sync.Mutex
	loggedOut     bool           // true if logged out (needs QR re-scan, don't auto-reconnect)
}

// MessageHandler is called when a trigger message is received.
type MessageHandler func(ctx context.Context, msg IncomingMessage)

// IncomingMessage represents a WhatsApp message that triggered the agent.
type IncomingMessage struct {
	ID        string      // Message ID
	ChatJID   string      // Chat JID (sender or group)
	SenderJID string      // Sender JID
	Body      string      // Message body (without trigger prefix)
	IsGroup   bool        // Whether message is from a group
	TaskName  string      // Parsed task name
	Mode      MessageMode // Which agent mode to use
}

// Config holds WhatsApp client configuration.
type Config struct {
	// Path to SQLite database for session storage
	DatabasePath string
	// Trigger prefix for OfficeClaw agent (e.g., "OC:")
	TriggerPrefix string
	// Trigger prefix for Claude CLI agent (e.g., "OCC:")
	ClaudeTrigger string
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
		cfg.TriggerPrefix = "OC:"
	}
	if cfg.ClaudeTrigger == "" {
		cfg.ClaudeTrigger = "OCC:"
	}
	if cfg.Logger == nil {
		cfg.Logger = log.New(os.Stdout, "[whatsapp] ", log.LstdFlags)
	}

	// Resolve machine name from hostname (first segment of FQDN)
	machineName := ""
	if hostname, err := os.Hostname(); err == nil {
		if idx := strings.IndexByte(hostname, '.'); idx != -1 {
			machineName = strings.ToLower(hostname[:idx])
		} else {
			machineName = strings.ToLower(hostname)
		}
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
		client:         client,
		container:      container,
		logger:         cfg.Logger,
		triggerPrefix:  cfg.TriggerPrefix,
		claudeTrigger:  cfg.ClaudeTrigger,
		copilotTrigger: "OCCO:",
		machineName:    machineName,
		shutdownCh:     make(chan struct{}),
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

// SetMessageHandler sets the callback for incoming OfficeClaw agent trigger messages.
func (c *Client) SetMessageHandler(handler MessageHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handler = handler
}

// SetClaudeHandler sets the callback for incoming Claude CLI agent trigger messages.
func (c *Client) SetClaudeHandler(handler MessageHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.claudeHandler = handler
}

// SetCopilotHandler sets the callback for incoming Copilot CLI agent trigger messages.
func (c *Client) SetCopilotHandler(handler MessageHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.copilotHandler = handler
}

// eventHandler processes incoming WhatsApp events.
func (c *Client) eventHandler(evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:
		c.handleMessage(v)
	case *events.Connected:
		c.connMu.Lock()
		c.connected = true
		c.connMu.Unlock()
		c.logger.Println("WhatsApp connected")
	case *events.Disconnected:
		c.connMu.Lock()
		c.connected = false
		c.connMu.Unlock()
		c.logger.Println("WhatsApp disconnected")
	case *events.LoggedOut:
		c.connMu.Lock()
		c.connected = false
		c.loggedOut = true
		c.connMu.Unlock()
		c.logger.Println("WhatsApp logged out - please restart and scan QR code (auto-reconnect disabled)")
	}
}

// StartReconnectWatchdog monitors the WhatsApp connection and reconnects if it drops.
// It runs until the context is cancelled (shutdown). Uses exponential backoff:
// 5s → 10s → 20s → 40s → 60s (max).
func (c *Client) StartReconnectWatchdog(ctx context.Context) {
	const (
		checkInterval  = 15 * time.Second
		initialBackoff = 5 * time.Second
		maxBackoff     = 60 * time.Second
	)

	backoff := initialBackoff

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.shutdownCh:
			return
		case <-time.After(checkInterval):
		}

		c.connMu.Lock()
		isConnected := c.connected
		isLoggedOut := c.loggedOut
		c.connMu.Unlock()

		if isLoggedOut {
			// Can't auto-reconnect after logout — needs QR code scan
			continue
		}

		if isConnected || c.client.IsConnected() {
			// Reset backoff on healthy connection
			backoff = initialBackoff
			continue
		}

		// Not connected — attempt reconnect
		c.logger.Printf("WhatsApp not connected, attempting reconnect (backoff: %v)...", backoff)

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		if err := c.client.Connect(); err != nil {
			c.logger.Printf("WhatsApp reconnect failed: %v", err)
			// Exponential backoff
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		} else {
			c.logger.Printf("WhatsApp reconnect initiated")
			backoff = initialBackoff
		}
	}
}

// handleMessage processes incoming messages and triggers the appropriate agent if prefix matches.
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

	textLower := strings.ToLower(text)

	// Determine which mode to use.
	// Check triggers longest-first to avoid prefix conflicts (e.g., "OC:" vs "OCC:" vs "OCP:")
	var mode MessageMode
	var prefix string
	var handler MessageHandler

	c.mu.RLock()
	claudeTriggerLower := strings.ToLower(c.claudeTrigger)
	copilotTriggerLower := strings.ToLower(c.copilotTrigger)
	triggerPrefixLower := strings.ToLower(c.triggerPrefix)

	// Build sorted trigger list (longer prefixes first)
	type triggerEntry struct {
		lower   string
		prefix  string
		mode    MessageMode
		handler MessageHandler
	}
	triggers := []triggerEntry{
		{claudeTriggerLower, c.claudeTrigger, ModeClaude, c.claudeHandler},
		{copilotTriggerLower, c.copilotTrigger, ModeCopilot, c.copilotHandler},
		{triggerPrefixLower, c.triggerPrefix, ModeOfficeClaw, c.handler},
	}
	// Sort by length descending (longest match first)
	for i := 0; i < len(triggers)-1; i++ {
		for j := i + 1; j < len(triggers); j++ {
			if len(triggers[j].lower) > len(triggers[i].lower) {
				triggers[i], triggers[j] = triggers[j], triggers[i]
			}
		}
	}

	matched := false
	for _, t := range triggers {
		if strings.HasPrefix(textLower, t.lower) {
			mode = t.mode
			prefix = t.prefix
			handler = t.handler
			matched = true
			break
		}
	}
	c.mu.RUnlock()

	if !matched {
		return
	}

	// Skip agent's own replies (messages from self that don't have trigger prefix were already filtered)
	// We allow trigger messages from self so you can control your own agent
	if msg.Info.IsFromMe {
		c.logger.Printf("Processing self-trigger message")
	}

	// Extract content after prefix (case-insensitive removal)
	content := text[len(prefix):]
	content = strings.TrimSpace(content)

	// Machine-targeted routing: check for @machine1,machine2 prefix
	// If the message has targeting syntax, only matching machines respond.
	// Machines without a configured name never match targeted messages.
	targets, remaining := ParseMachineTarget(content)
	if targets != nil {
		matched := false
		if c.machineName != "" {
			for _, t := range targets {
				if strings.EqualFold(t, c.machineName) {
					matched = true
					break
				}
			}
		}
		if !matched {
			c.logger.Printf("Machine routing: message targets %v, this machine is %q — skipping", targets, c.machineName)
			return
		}
		content = remaining
	}

	// Parse task name (first word) or use default - only for OfficeClaw mode
	taskName := "assist"
	if mode == ModeOfficeClaw {
		parts := strings.Fields(content)
		if len(parts) > 0 {
			taskName = parts[0]
			content = strings.TrimSpace(strings.TrimPrefix(content, taskName))
		}
	}

	c.logger.Printf("Trigger message from %s: mode=%s, task=%s", msg.Info.Sender.User, mode, taskName)

	if handler != nil {
		// Reject new messages during shutdown
		select {
		case <-c.shutdownCh:
			c.logger.Printf("Rejecting message during shutdown from %s", msg.Info.Sender.User)
			return
		default:
		}

		incoming := IncomingMessage{
			ID:        msg.Info.ID,
			ChatJID:   msg.Info.Chat.String(),
			SenderJID: msg.Info.Sender.String(),
			Body:      content,
			IsGroup:   msg.Info.IsGroup,
			TaskName:  taskName,
			Mode:      mode,
		}
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			handler(context.Background(), incoming)
		}()
	} else {
		c.logger.Printf("No handler registered for mode %s", mode)
		// Reply to user so they know the agent mode is unavailable
		var reply string
		switch mode {
		case ModeOfficeClaw:
			reply = "OC: agent is not available. No LLM provider configured (check llm.provider in config)."
		case ModeClaude:
			reply = "Claude CLI agent is not available. Ensure the Claude CLI is installed and authenticated."
		case ModeCopilot:
			reply = "Copilot CLI agent is not available. Ensure the Copilot CLI is installed and authenticated."
		default:
			reply = fmt.Sprintf("No handler available for mode %s.", mode)
		}
		chatJID := msg.Info.Chat.String()
		go func() {
			if err := c.SendMessage(context.Background(), chatJID, reply); err != nil {
				c.logger.Printf("Failed to send unavailable-agent reply: %v", err)
			}
		}()
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

// GracefulDisconnect stops accepting new messages, waits for in-flight handlers
// to complete (up to timeout), then disconnects.
func (c *Client) GracefulDisconnect(timeout time.Duration) {
	// Signal shutdown to stop accepting new messages
	select {
	case <-c.shutdownCh:
		// Already shutting down
	default:
		close(c.shutdownCh)
	}

	c.logger.Printf("Waiting for in-flight handlers (timeout: %v)...", timeout)

	// Wait for in-flight handlers with timeout
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		c.logger.Printf("All handlers completed")
	case <-time.After(timeout):
		c.logger.Printf("Shutdown timeout: some handlers still running")
	}

	c.client.Disconnect()
}

// Disconnect disconnects from WhatsApp immediately.
func (c *Client) Disconnect() {
	c.client.Disconnect()
}

// InFlightCount returns the number of in-flight handlers (approximate).
// This is used for logging during shutdown.
func (c *Client) InFlightCount() int {
	// WaitGroup doesn't expose count, so we use a non-blocking check
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return 0
	default:
		return -1 // unknown but non-zero
	}
}

// IsConnected returns whether the client is connected.
func (c *Client) IsConnected() bool {
	return c.client.IsConnected()
}

// MachineName returns the resolved machine name (short hostname).
func (c *Client) MachineName() string {
	return c.machineName
}

// ParseMachineTarget parses the @machine targeting syntax from message content.
// If content starts with "@" followed by one or more machine names (comma-separated),
// it returns the target list and the remaining message body.
// Returns nil targets if there is no targeting (all machines should respond).
func ParseMachineTarget(content string) (targets []string, remaining string) {
	if !strings.HasPrefix(content, "@") {
		return nil, content
	}

	// Find the end of the @token (first whitespace or end of string)
	token := content[1:] // skip the @
	endIdx := -1
	for i, r := range token {
		if unicode.IsSpace(r) {
			endIdx = i
			break
		}
	}

	var targetStr string
	if endIdx == -1 {
		// @token is the entire content
		targetStr = token
		remaining = ""
	} else {
		targetStr = token[:endIdx]
		remaining = strings.TrimSpace(token[endIdx:])
	}

	// Bare "@" with no name = untargeted
	if targetStr == "" {
		return nil, content
	}

	// Split by comma
	parts := strings.Split(targetStr, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			targets = append(targets, p)
		}
	}

	if len(targets) == 0 {
		return nil, content
	}

	return targets, remaining
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
