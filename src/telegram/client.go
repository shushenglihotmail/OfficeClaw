// Package telegram provides Telegram Bot API integration using long polling.
package telegram

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// MessageMode indicates which agent mode to use for a message.
type MessageMode string

const (
	ModeOfficeClaw MessageMode = "officeclaw" // Custom OfficeClaw agent
	ModeClaude     MessageMode = "claude"     // Direct Claude CLI agent
	ModeCopilot    MessageMode = "copilot"    // Direct Copilot CLI agent
)

// maxMessageLen is Telegram's maximum message length.
const maxMessageLen = 4096

// Client wraps the Telegram Bot API client.
type Client struct {
	bot            *tgbotapi.BotAPI
	logger         *log.Logger
	triggerPrefix  string       // e.g., "OC:"
	claudeTrigger  string       // e.g., "OCC:"
	copilotTrigger string       // hardcoded "OCCO:"
	machineName    string       // short hostname, resolved at startup (for targeted messaging)
	handler        MessageHandler
	claudeHandler  MessageHandler
	copilotHandler MessageHandler
	muted          bool           // true when instance is muted via /mute command
	mu             sync.RWMutex
	wg             sync.WaitGroup // tracks in-flight message handlers
	shutdownCh     chan struct{}   // closed when shutdown starts

	// Allowed chat IDs for access control (empty = allow all)
	allowedChatIDs map[int64]bool

	// Polling state
	connected    bool
	connMu       sync.Mutex
	stopPolling  context.CancelFunc
}

// MessageHandler is called when a trigger message is received.
type MessageHandler func(ctx context.Context, msg IncomingMessage)

// IncomingMessage represents a Telegram message that triggered the agent.
type IncomingMessage struct {
	ID       string      // Message ID
	ChatID   string      // Chat ID (as string for caller compatibility)
	SenderID string      // Sender ID (as string)
	Body     string      // Message body (without trigger prefix)
	IsGroup  bool        // Whether message is from a group
	TaskName string      // Parsed task name
	Mode     MessageMode // Which agent mode to use
}

// Config holds Telegram client configuration.
type Config struct {
	// Bot token from @BotFather
	BotToken string
	// Trigger prefix for OfficeClaw agent (e.g., "OC:")
	TriggerPrefix string
	// Trigger prefix for Claude CLI agent (e.g., "OCC:")
	ClaudeTrigger string
	// Default task when none specified
	DefaultTask string
	// Logger
	Logger *log.Logger
	// Allowed chat IDs (empty = allow all chats)
	AllowedChatIDs []int64
}

// New creates a new Telegram bot client.
func New(cfg Config) (*Client, error) {
	if cfg.BotToken == "" {
		return nil, fmt.Errorf("telegram bot token is required (set telegram.bot_token in config or TELEGRAM_BOT_TOKEN env)")
	}
	if cfg.TriggerPrefix == "" {
		cfg.TriggerPrefix = "OC:"
	}
	if cfg.ClaudeTrigger == "" {
		cfg.ClaudeTrigger = "OCC:"
	}
	if cfg.Logger == nil {
		cfg.Logger = log.New(os.Stdout, "[telegram] ", log.LstdFlags)
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

	// Create Telegram bot API client
	bot, err := tgbotapi.NewBotAPI(cfg.BotToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create Telegram bot: %w", err)
	}

	cfg.Logger.Printf("Authorized as Telegram bot @%s", bot.Self.UserName)

	// Build allowed chat ID set
	allowed := make(map[int64]bool, len(cfg.AllowedChatIDs))
	for _, id := range cfg.AllowedChatIDs {
		allowed[id] = true
	}

	return &Client{
		bot:            bot,
		logger:         cfg.Logger,
		triggerPrefix:  cfg.TriggerPrefix,
		claudeTrigger:  cfg.ClaudeTrigger,
		copilotTrigger: "OCCO:",
		machineName:    machineName,
		allowedChatIDs: allowed,
		shutdownCh:     make(chan struct{}),
	}, nil
}

// Connect starts long-polling for updates from Telegram.
func (c *Client) Connect(ctx context.Context) error {
	pollCtx, pollCancel := context.WithCancel(ctx)
	c.stopPolling = pollCancel

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30

	updates := c.bot.GetUpdatesChan(u)

	c.connMu.Lock()
	c.connected = true
	c.connMu.Unlock()

	c.logger.Printf("Telegram bot connected, listening for updates...")

	go func() {
		defer func() {
			c.connMu.Lock()
			c.connected = false
			c.connMu.Unlock()
		}()

		for {
			select {
			case <-pollCtx.Done():
				return
			case update, ok := <-updates:
				if !ok {
					return
				}
				if update.Message != nil && update.Message.Text != "" {
					c.handleMessage(update.Message)
				}
			}
		}
	}()

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

// handleMessage processes incoming messages and triggers the appropriate agent if prefix matches.
func (c *Client) handleMessage(msg *tgbotapi.Message) {
	text := msg.Text
	chatID := msg.Chat.ID
	chatIDStr := strconv.FormatInt(chatID, 10)

	// Check allowed chat IDs (if configured)
	if len(c.allowedChatIDs) > 0 {
		if !c.allowedChatIDs[chatID] {
			c.logger.Printf("Ignoring message from unauthorized chat %d (sender: %s)", chatID, msg.From.UserName)
			return
		}
	} else {
		// Log chat ID for easy setup of allowed_chat_ids
		c.logger.Printf("Message from chat %d (sender: %s)", chatID, msg.From.UserName)
	}

	textLower := strings.ToLower(text)

	// Determine which mode to use.
	// Check triggers longest-first to avoid prefix conflicts (e.g., "OC:" vs "OCC:" vs "OCCO:")
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

	c.logger.Printf("Matched trigger %q (mode=%s) for message: %s", prefix, mode, truncateLog(text, 80))

	// Extract content after prefix (case-insensitive removal)
	content := text[len(prefix):]
	content = strings.TrimSpace(content)

	// Machine-targeted routing: check for @machine1,machine2 prefix
	targets, remaining := ParseMachineTarget(content)
	if targets != nil {
		targetMatched := false
		if c.machineName != "" {
			for _, t := range targets {
				if strings.EqualFold(t, c.machineName) {
					targetMatched = true
					break
				}
			}
		}
		if !targetMatched {
			c.logger.Printf("Machine routing: message targets %v, this machine is %q — skipping", targets, c.machineName)
			return
		}
		content = remaining
	}

	// Global commands: /ping, /unmute, /mute — handled before any agent dispatch.
	contentLower := strings.ToLower(strings.TrimSpace(content))
	senderName := msg.From.UserName
	if senderName == "" {
		senderName = strconv.FormatInt(msg.From.ID, 10)
	}

	if contentLower == "/ping" {
		c.handlePingCommand(chatIDStr)
		return
	}
	if contentLower == "/unmute" {
		c.SetMuted(false)
		c.logger.Printf("Unmuted by %s", senderName)
		go func() {
			if err := c.SendMessage(context.Background(), chatIDStr, fmt.Sprintf("Machine %s is now unmuted.", c.machineName)); err != nil {
				c.logger.Printf("Failed to send unmute reply: %v", err)
			}
		}()
		return
	}
	if contentLower == "/mute" {
		c.SetMuted(true)
		c.logger.Printf("Muted by %s", senderName)
		go func() {
			if err := c.SendMessage(context.Background(), chatIDStr, "I am in mute state now. I will only respond to /unmute and /ping commands."); err != nil {
				c.logger.Printf("Failed to send mute reply: %v", err)
			}
		}()
		return
	}
	if c.IsMuted() {
		c.logger.Printf("Muted: ignoring message from %s", senderName)
		return
	}

	// Parse task name (first word) or use default - only for OfficeClaw mode
	// Skip task parsing if content starts with "/" — it's a slash command, not a task name.
	taskName := "assist"
	if mode == ModeOfficeClaw && !strings.HasPrefix(content, "/") {
		parts := strings.Fields(content)
		if len(parts) > 0 {
			taskName = parts[0]
			content = strings.TrimSpace(strings.TrimPrefix(content, taskName))
		}
	}

	c.logger.Printf("Trigger message from %s: mode=%s, task=%s", senderName, mode, taskName)

	if handler != nil {
		// Reject new messages during shutdown
		select {
		case <-c.shutdownCh:
			c.logger.Printf("Rejecting message during shutdown from %s", senderName)
			return
		default:
		}

		incoming := IncomingMessage{
			ID:       strconv.Itoa(msg.MessageID),
			ChatID:   chatIDStr,
			SenderID: strconv.FormatInt(msg.From.ID, 10),
			Body:     content,
			IsGroup:  msg.Chat.IsGroup() || msg.Chat.IsSuperGroup(),
			TaskName: taskName,
			Mode:     mode,
		}
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			handler(context.Background(), incoming)
		}()
	} else {
		c.logger.Printf("No handler registered for mode %s", mode)
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
		go func() {
			if err := c.SendMessage(context.Background(), chatIDStr, reply); err != nil {
				c.logger.Printf("Failed to send unavailable-agent reply: %v", err)
			}
		}()
	}
}

// SendMessage sends a text message to a chat. Auto-splits messages longer than 4096 chars.
func (c *Client) SendMessage(ctx context.Context, chatID string, text string) error {
	id, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid chat ID %s: %w", chatID, err)
	}

	// Split message if it exceeds Telegram's limit
	parts := splitMessage(text, maxMessageLen)

	for i, part := range parts {
		msg := tgbotapi.NewMessage(id, part)
		if _, err := c.bot.Send(msg); err != nil {
			return fmt.Errorf("failed to send message (part %d/%d): %w", i+1, len(parts), err)
		}

		// Small delay between parts for rate-limit safety
		if i < len(parts)-1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
	}

	c.logger.Printf("Sent message to %s (%d chars, %d parts)", chatID, len(text), len(parts))
	return nil
}

// splitMessage splits a message at newline boundaries to respect the max length.
func splitMessage(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}

	var parts []string
	for len(text) > 0 {
		if len(text) <= maxLen {
			parts = append(parts, text)
			break
		}

		// Find the last newline within the limit
		cutAt := maxLen
		if idx := strings.LastIndex(text[:maxLen], "\n"); idx > 0 {
			cutAt = idx + 1 // include the newline in this part
		}

		parts = append(parts, text[:cutAt])
		text = text[cutAt:]
	}

	return parts
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

	// Stop polling
	if c.stopPolling != nil {
		c.stopPolling()
	}
	c.bot.StopReceivingUpdates()
}

// Disconnect disconnects from Telegram immediately.
func (c *Client) Disconnect() {
	if c.stopPolling != nil {
		c.stopPolling()
	}
	c.bot.StopReceivingUpdates()
}

// StartReconnectWatchdog monitors the polling loop and restarts if needed.
// Simplified — no "logged out" concept with Telegram bots.
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
		c.connMu.Unlock()

		if isConnected {
			backoff = initialBackoff
			continue
		}

		// Not connected — attempt reconnect
		c.logger.Printf("Telegram not connected, attempting reconnect (backoff: %v)...", backoff)

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		if err := c.Connect(ctx); err != nil {
			c.logger.Printf("Telegram reconnect failed: %v", err)
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		} else {
			c.logger.Printf("Telegram reconnect initiated")
			backoff = initialBackoff
		}
	}
}

// IsConnected returns whether the client is connected.
func (c *Client) IsConnected() bool {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	return c.connected
}

// MachineName returns the resolved machine name (short hostname).
func (c *Client) MachineName() string {
	return c.machineName
}

// IsMuted returns whether this instance is muted.
func (c *Client) IsMuted() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.muted
}

// SetMuted sets the muted state of this instance.
func (c *Client) SetMuted(muted bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.muted = muted
}

// handlePingCommand sends a ping response with machine info to the given chat.
func (c *Client) handlePingCommand(chatID string) {
	state := "active"
	if c.IsMuted() {
		state = "muted"
	}

	// Check which modes are available based on registered handlers
	c.mu.RLock()
	ocAvail := c.handler != nil
	occAvail := c.claudeHandler != nil
	occoAvail := c.copilotHandler != nil
	c.mu.RUnlock()

	check := func(avail bool) string {
		if avail {
			return "✓"
		}
		return "✗"
	}

	reply := fmt.Sprintf("Machine: %s\nState: %s\nModes: OC %s | OCC %s | OCCO %s",
		c.machineName, state,
		check(ocAvail), check(occAvail), check(occoAvail))

	go func() {
		if err := c.SendMessage(context.Background(), chatID, reply); err != nil {
			c.logger.Printf("Failed to send ping reply: %v", err)
		}
	}()
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

// truncateLog shortens a string for log output.
func truncateLog(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}
