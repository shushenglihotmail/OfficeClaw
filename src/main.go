// Package main is the entry point for the OfficeClaw AI agent.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/officeclaw/src/agent"
	"github.com/officeclaw/src/config"
	"github.com/officeclaw/src/llm"
	"github.com/officeclaw/src/mcp"
	"github.com/officeclaw/src/memory"
	"github.com/officeclaw/src/pending"
	"github.com/officeclaw/src/tasks"
	"github.com/officeclaw/src/telegram"
	"github.com/officeclaw/src/telemetry"
	"github.com/officeclaw/src/tools"
	"github.com/officeclaw/src/tray"
)

func main() {
	// Check for subcommands before flag parsing
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "mcp":
			if len(os.Args) >= 3 && os.Args[2] == "serve" {
				runMCPServer()
				return
			}
		}
	}

	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	flag.Parse()

	runInteractive(*configPath)
}

// runInteractive runs OfficeClaw in interactive mode with system tray.
func runInteractive(configPath string) {
	ctx, cancel := context.WithCancel(context.Background())
	runApp(ctx, cancel, configPath)
}

// runApp is the core application logic.
func runApp(ctx context.Context, cancel context.CancelFunc, configPath string) {
	// Load configuration
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Initialize logging
	logger := setupLogging(cfg.Logging)
	logger.Printf("OfficeClaw starting...")

	// Initialize telemetry (OpenTelemetry + Prometheus)
	tp, err := telemetry.Init(cfg.Telemetry)
	if err != nil {
		log.Fatalf("Failed to init telemetry: %v", err)
	}
	defer tp.Shutdown(context.Background())
	logger.Printf("Telemetry initialized (prometheus=%v, otel=%v)",
		cfg.Telemetry.Prometheus.Enabled, cfg.Telemetry.OTel.Enabled)

	// Initialize pending message queue
	pendingQueue := pending.NewQueue("pending_messages.json", logger)
	if n := pendingQueue.Len(); n > 0 {
		logger.Printf("Found %d pending messages from previous session", n)
	}

	// Initialize Telegram bot client
	tgClient, err := telegram.New(telegram.Config{
		BotToken:       cfg.Telegram.BotToken,
		TriggerPrefix:  cfg.Telegram.TriggerPrefix,
		ClaudeTrigger:  cfg.Telegram.ClaudeTrigger,
		DefaultTask:    cfg.Telegram.DefaultTask,
		Logger:         logger,
		AllowedChatIDs: cfg.Telegram.AllowedChatIDs,
	})
	if err != nil {
		log.Fatalf("Failed to init Telegram bot: %v", err)
	}

	// Connect to Telegram (starts long-polling)
	logger.Printf("Connecting to Telegram...")
	if err := tgClient.Connect(ctx); err != nil {
		log.Fatalf("Failed to connect to Telegram: %v", err)
	}
	logger.Printf("Telegram bot connected")

	// Drain pending messages after connecting
	if pendingQueue.Len() > 0 {
		logger.Printf("Draining pending messages...")
		pendingQueue.Drain(ctx, tgClient, 24*time.Hour)
	}

	// Initialize LLM client for OC: mode (optional — OCC:/OCCO: modes don't use it)
	var llmClient *llm.Client
	if cfg.LLM.Provider != "" {
		llmClient, err = llm.NewClient(cfg.LLM)
		if err != nil {
			logger.Printf("Warning: LLM client not available (provider %q): %v", cfg.LLM.Provider, err)
			logger.Printf("OC: mode will be unavailable. OCC:/OCCO: modes are unaffected.")
		} else {
			logger.Printf("LLM client initialized (provider: %s)", cfg.LLM.Provider)
		}
	} else {
		logger.Printf("No LLM provider configured (llm.provider empty). OC: mode unavailable.")
	}

	// Initialize task registry and executor
	taskRegistry := tasks.NewRegistry()
	for name, taskCfg := range cfg.Tasks {
		taskRegistry.Register(name, taskCfg)
	}
	taskExecutor := tasks.NewExecutor(taskRegistry, logger)
	logger.Printf("Task executor initialized (%d tasks registered)", taskRegistry.Count())

	// Initialize tool registry
	toolRegistry := tools.NewRegistry()

	// Create task execution tool (needs Telegram client for async notifications)
	var taskExecTool *tools.TaskExecutionTool
	if cfg.Tools.TaskExecution.Enabled {
		taskExecTool = tools.NewTaskExecutionTool(taskExecutor, tgClient)
		toolRegistry.Register(taskExecTool)
	}

	// Register other built-in tools
	if cfg.Tools.Messaging.Enabled {
		toolRegistry.Register(tools.NewMessagingTool(tgClient))
	}
	if cfg.Tools.FileAccess.Enabled {
		toolRegistry.Register(tools.NewFileAccessTool(cfg.Tools.FileAccess))
	}
	if cfg.Tools.VPN.Enabled {
		toolRegistry.Register(tools.NewVPNTool(cfg.Tools.VPN))
	}

	// Task log viewing tool (always enabled if task execution is enabled)
	if cfg.Tools.TaskExecution.Enabled {
		toolRegistry.Register(tools.NewTaskLogTool(taskExecutor))
	}

	// Identity tool (always registered — lightweight read-only tool)
	toolRegistry.Register(tools.NewIdentityTool(tgClient.MachineName()))

	// Initialize memory client (optional - graceful degradation if service not available)
	var memoryClient *memory.Client
	if cfg.Tools.Memory.ServiceURL != "" {
		memoryClient = memory.NewClient(cfg.Tools.Memory.ServiceURL)
		if err := memoryClient.HealthCheck(ctx); err != nil {
			logger.Printf("Memory service not reachable at %s: %v", cfg.Tools.Memory.ServiceURL, err)
			logger.Printf("Memory features disabled")
			memoryClient = nil
		} else {
			logger.Printf("Memory service connected at %s", cfg.Tools.Memory.ServiceURL)
			// Register memory tools
			toolRegistry.Register(tools.NewMemorySearchTool(memoryClient))
			toolRegistry.Register(tools.NewMemoryWriteTool(memoryClient))
		}
	} else {
		logger.Printf("Memory service not configured (service_url empty)")
	}

	logger.Printf("Tool registry initialized (%d tools registered)", toolRegistry.Count())

	// Create the core agent and OC: handler (only if LLM client is available)
	if llmClient != nil {
		agentInstance := agent.New(agent.Config{
			LLMClient:        llmClient,
			ToolRegistry:     toolRegistry,
			TaskExecutor:     taskExecutor,
			MemoryClient:     memoryClient,
			Logger:           logger,
			DefaultTask:      cfg.Telegram.DefaultTask,
			MaxContextTokens: cfg.Tools.Memory.MaxContextTokens,
			FlushThreshold:   cfg.Tools.Memory.FlushThreshold,
		})

		tgClient.SetMessageHandler(func(ctx context.Context, msg telegram.IncomingMessage) {
			// Check for slash commands
			if cmd := agent.ParseCommand(msg.Body); cmd != nil {
				var reply string
				switch cmd.Name {
				case "reset", "clear":
					agentInstance.ClearSession()
					reply = "Session cleared. Conversation context has been reset."
				case "summary":
					result, err := agentInstance.ForceSummary(ctx)
					if err != nil {
						reply = fmt.Sprintf("Summary failed: %v", err)
					} else {
						reply = result
					}
				case "help":
					reply = agent.CommandHelpText("OC")
				default:
					reply = fmt.Sprintf("Unknown command: /%s\n\n%s", cmd.Name, agent.CommandHelpText("OC"))
				}
				if err := tgClient.SendMessage(ctx, msg.ChatID, reply); err != nil {
					logger.Printf("Failed to send command reply: %v", err)
				}
				return
			}

			// Set chat ID for async task notifications
			if taskExecTool != nil {
				taskExecTool.SetChatID(msg.ChatID)
			}

			agentInstance.HandleMessage(ctx, agent.IncomingMessage{
				Source:    "telegram",
				SenderID:  msg.SenderID,
				Sender:    msg.SenderID,
				Subject:   "",
				Body:      msg.Body,
				ChatID:    msg.ChatID,
				MessageID: msg.ID,
				Task:      msg.TaskName,
			})
		})
		logger.Printf("OC: agent active (trigger: %s)", cfg.Telegram.TriggerPrefix)
	}
	logger.Printf("Telegram listener active (trigger: %s)", cfg.Telegram.TriggerPrefix)

	// Initialize Claude CLI agent for direct Claude mode (persistent session)
	claudeAgent, err := agent.NewClaudeAgent(agent.ClaudeAgentConfig{
		CLIPath:       cfg.LLM.Anthropic.CLIPath,
		WorkingFolder: cfg.Telegram.ClaudeWorkingFolder,
		TGClient:      tgClient,
		MemoryClient:  memoryClient,
		PendingQueue:  pendingQueue,
		Logger:        logger,
		ResetKeyword:  cfg.Telegram.ClaudeSessionResetKeyword,
	})
	if err != nil {
		logger.Printf("Warning: Claude CLI agent not available: %v", err)
	} else {
		tgClient.SetClaudeHandler(claudeAgent.HandleMessage)
		logger.Printf("Claude CLI agent active (trigger: %s, folder: %s, reset: %q)",
			cfg.Telegram.ClaudeTrigger, cfg.Telegram.ClaudeWorkingFolder, cfg.Telegram.ClaudeSessionResetKeyword)
	}

	// Initialize Copilot CLI agent for direct Copilot mode (persistent session)
	copilotAgent, err := agent.NewCopilotAgent(agent.CopilotAgentConfig{
		CLIPath:       cfg.LLM.Copilot.CLIPath,
		Model:         cfg.LLM.Copilot.Model,
		WorkingFolder: cfg.Telegram.CopilotWorkingFolder,
		TGClient:      tgClient,
		MemoryClient:  memoryClient,
		PendingQueue:  pendingQueue,
		Logger:        logger,
		ResetKeyword:  cfg.Telegram.ClaudeSessionResetKeyword, // Reuse same reset keyword
	})
	if err != nil {
		logger.Printf("Warning: Copilot CLI agent not available: %v", err)
	} else {
		tgClient.SetCopilotHandler(copilotAgent.HandleMessage)
		logger.Printf("Copilot CLI agent active (trigger: OCCO:, folder: %s)",
			cfg.Telegram.CopilotWorkingFolder)
	}

	// Start task scheduler
	go taskExecutor.StartScheduler(ctx)

	// Start Telegram reconnection watchdog
	go tgClient.StartReconnectWatchdog(ctx)

	// Setup signal handler
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		logger.Printf("Shutdown signal received")
		cancel()
	}()

	// System tray blocks on main thread (Windows GUI requirement)
	// When tray exits, we proceed to shutdown
	logger.Printf("OfficeClaw is running. Minimized to system tray.")
	tray.Run(cfg, cancel, logger)

	// === Graceful shutdown sequence ===
	logger.Printf("Initiating graceful shutdown...")

	// 1. Stop CLI agents (cancel running CLI sessions)
	if claudeAgent != nil {
		claudeAgent.Stop()
	}
	if copilotAgent != nil {
		copilotAgent.Stop()
	}

	// 2. Wait for in-flight Telegram handlers, then disconnect
	tgClient.GracefulDisconnect(30 * time.Second)

	// 3. Save any pending messages that couldn't be sent
	// (handlers that completed but failed to send due to disconnect)
	logger.Printf("Graceful shutdown complete")
}

// logFile holds the log file handle for the application
var logFile *os.File

// setupLogging creates a logger that writes to a log file.
// In GUI mode (no console), output goes only to the file.
func setupLogging(cfg config.LoggingConfig) *log.Logger {
	var output io.Writer = os.Stdout

	if cfg.File != "" {
		f, err := os.OpenFile(cfg.File, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			// Fall back to stdout if file can't be opened
			log.Printf("Warning: could not open log file %s: %v", cfg.File, err)
		} else {
			logFile = f
			// Write to file only (GUI mode has no console)
			output = f
			// Also redirect standard log package and fmt output to file
			log.SetOutput(f)
		}
	}

	return log.New(output, "[OfficeClaw] ", log.LstdFlags|log.Lmsgprefix)
}

// runMCPServer runs the MCP server in standalone mode.
// This is used when OfficeClaw is spawned by Claude CLI as an MCP server.
func runMCPServer() {
	// MCP uses stdio for communication, so logs go to stderr
	logger := log.New(os.Stderr, "[mcp] ", log.LstdFlags|log.Lmsgprefix)

	// Load config from environment variable or default path
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "config.yaml"
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		logger.Fatalf("Failed to load config: %v", err)
	}

	// Initialize task registry (for execute_task tool)
	taskRegistry := tasks.NewRegistry()
	for name, taskCfg := range cfg.Tasks {
		taskRegistry.Register(name, taskCfg)
	}
	taskExecutor := tasks.NewExecutor(taskRegistry, logger)

	// Initialize tool registry with tools that don't require Telegram
	toolRegistry := tools.NewRegistry()

	if cfg.Tools.FileAccess.Enabled {
		toolRegistry.Register(tools.NewFileAccessTool(cfg.Tools.FileAccess))
	}
	if cfg.Tools.TaskExecution.Enabled {
		// In MCP mode, no Telegram client available, so pass nil
		// Async notifications won't work, but sync execution will
		toolRegistry.Register(tools.NewTaskExecutionTool(taskExecutor, nil))
	}
	if cfg.Tools.VPN.Enabled {
		toolRegistry.Register(tools.NewVPNTool(cfg.Tools.VPN))
	}

	// Task log viewing tool (always enabled if task execution is enabled)
	if cfg.Tools.TaskExecution.Enabled {
		toolRegistry.Register(tools.NewTaskLogTool(taskExecutor))
	}

	// Identity tool (always registered — lightweight read-only tool)
	toolRegistry.Register(tools.NewIdentityTool(resolveShortHostname()))

	// Initialize memory client for MCP server
	if cfg.Tools.Memory.ServiceURL != "" {
		memoryClient := memory.NewClient(cfg.Tools.Memory.ServiceURL)
		ctx := context.Background()
		if err := memoryClient.HealthCheck(ctx); err != nil {
			logger.Printf("Memory service not reachable at %s: %v", cfg.Tools.Memory.ServiceURL, err)
		} else {
			logger.Printf("Memory service connected at %s", cfg.Tools.Memory.ServiceURL)
			toolRegistry.Register(tools.NewMemorySearchTool(memoryClient))
			toolRegistry.Register(tools.NewMemoryWriteTool(memoryClient))
		}
	}

	// Note: send_message tool requires Telegram client which isn't available in standalone mode
	// For full tool access, run OfficeClaw normally and use OCC: mode

	logger.Printf("MCP server starting with %d tools", toolRegistry.Count())

	// Create and run MCP server
	server := mcp.NewServer(toolRegistry, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		logger.Println("Shutdown signal received")
		cancel()
	}()

	if err := server.Run(ctx); err != nil {
		logger.Fatalf("MCP server error: %v", err)
	}
}

// resolveShortHostname returns the lowercase short hostname (first segment of FQDN).
func resolveShortHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		return ""
	}
	if idx := strings.IndexByte(hostname, '.'); idx != -1 {
		return strings.ToLower(hostname[:idx])
	}
	return strings.ToLower(hostname)
}
