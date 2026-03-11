// Package main is the entry point for the OfficeClaw AI agent.
package main

import (
	"context"
	"flag"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/officeclaw/src/agent"
	"github.com/officeclaw/src/config"
	"github.com/officeclaw/src/llm"
	"github.com/officeclaw/src/mcp"
	"github.com/officeclaw/src/memory"
	"github.com/officeclaw/src/tasks"
	"github.com/officeclaw/src/telemetry"
	"github.com/officeclaw/src/tools"
	"github.com/officeclaw/src/tray"
	"github.com/officeclaw/src/whatsapp"
)

func main() {
	// Check for MCP subcommand before flag parsing
	if len(os.Args) >= 3 && os.Args[1] == "mcp" && os.Args[2] == "serve" {
		runMCPServer()
		return
	}

	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	flag.Parse()

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Initialize logging
	logger := setupLogging(cfg.Logging)
	logger.Printf("OfficeClaw starting...")

	// Initialize telemetry (OpenTelemetry + Prometheus)
	tp, err := telemetry.Init(cfg.Telemetry)
	if err != nil {
		logger.Fatalf("Failed to init telemetry: %v", err)
	}
	defer tp.Shutdown(context.Background())
	logger.Printf("Telemetry initialized (prometheus=%v, otel=%v)",
		cfg.Telemetry.Prometheus.Enabled, cfg.Telemetry.OTel.Enabled)

	// Initialize WhatsApp client
	waClient, err := whatsapp.New(whatsapp.Config{
		DatabasePath:  cfg.WhatsApp.DatabasePath,
		TriggerPrefix: cfg.WhatsApp.TriggerPrefix,
		ClaudeTrigger: cfg.WhatsApp.ClaudeTrigger,
		DefaultTask:   cfg.WhatsApp.DefaultTask,
		Logger:        logger,
	})
	if err != nil {
		logger.Fatalf("Failed to init WhatsApp client: %v", err)
	}

	// Connect to WhatsApp (may show QR code on first run)
	ctx := context.Background()
	logger.Printf("Connecting to WhatsApp...")
	if err := waClient.Connect(ctx); err != nil {
		logger.Fatalf("Failed to connect to WhatsApp: %v", err)
	}
	logger.Printf("WhatsApp connected")

	// Initialize LLM client (uses Claude CLI with SSO auth - no API key needed)
	llmClient, err := llm.NewClient(cfg.LLM)
	if err != nil {
		logger.Fatalf("Failed to init LLM client: %v", err)
	}
	logger.Printf("LLM client initialized (provider: %s)", cfg.LLM.Provider)

	// Initialize task registry and executor
	taskRegistry := tasks.NewRegistry()
	for name, taskCfg := range cfg.Tasks {
		taskRegistry.Register(name, taskCfg)
	}
	taskExecutor := tasks.NewExecutor(taskRegistry, logger)
	logger.Printf("Task executor initialized (%d tasks registered)", taskRegistry.Count())

	// Initialize tool registry
	toolRegistry := tools.NewRegistry()

	// Create task execution tool (needs WhatsApp client for async notifications)
	var taskExecTool *tools.TaskExecutionTool
	if cfg.Tools.TaskExecution.Enabled {
		taskExecTool = tools.NewTaskExecutionTool(taskExecutor, waClient)
		toolRegistry.Register(taskExecTool)
	}

	// Register other built-in tools
	if cfg.Tools.Messaging.Enabled {
		toolRegistry.Register(tools.NewMessagingTool(waClient))
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

	// Create the core agent
	agentInstance := agent.New(agent.Config{
		LLMClient:        llmClient,
		ToolRegistry:     toolRegistry,
		TaskExecutor:     taskExecutor,
		MemoryClient:     memoryClient,
		Logger:           logger,
		DefaultTask:      cfg.WhatsApp.DefaultTask,
		MaxContextTokens: cfg.Tools.Memory.MaxContextTokens,
		FlushThreshold:   cfg.Tools.Memory.FlushThreshold,
	})

	// Setup context with cancellation for graceful shutdown
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Bridge WhatsApp messages to OfficeClaw agent
	waClient.SetMessageHandler(func(ctx context.Context, msg whatsapp.IncomingMessage) {
		// Set chat JID for async task notifications
		if taskExecTool != nil {
			taskExecTool.SetChatJID(msg.ChatJID)
		}

		agentInstance.HandleMessage(ctx, agent.IncomingMessage{
			Source:    "whatsapp",
			SenderID:  msg.SenderJID,
			Sender:    msg.SenderJID,
			Subject:   "",
			Body:      msg.Body,
			ChatID:    msg.ChatJID,
			MessageID: msg.ID,
			Task:      msg.TaskName,
		})
	})
	logger.Printf("WhatsApp listener active (trigger: %s)", cfg.WhatsApp.TriggerPrefix)

	// Initialize Claude CLI agent for direct Claude mode (persistent session)
	claudeAgent, err := agent.NewClaudeAgent(agent.ClaudeAgentConfig{
		CLIPath:       cfg.LLM.Anthropic.CLIPath,
		WorkingFolder: cfg.WhatsApp.ClaudeWorkingFolder,
		WAClient:      waClient,
		MemoryClient:  memoryClient,
		Logger:        logger,
		ResetKeyword:  cfg.WhatsApp.ClaudeSessionResetKeyword,
	})
	if err != nil {
		logger.Printf("Warning: Claude CLI agent not available: %v", err)
	} else {
		waClient.SetClaudeHandler(claudeAgent.HandleMessage)
		logger.Printf("Claude CLI agent active (trigger: %s, folder: %s, reset: %q)",
			cfg.WhatsApp.ClaudeTrigger, cfg.WhatsApp.ClaudeWorkingFolder, cfg.WhatsApp.ClaudeSessionResetKeyword)
	}

	// Start task scheduler
	go taskExecutor.StartScheduler(ctx)

	// Start system tray (blocks on main thread on Windows)
	go func() {
		// Listen for shutdown signals
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		logger.Printf("Shutdown signal received")
		if claudeAgent != nil {
			claudeAgent.Stop()
		}
		waClient.Disconnect()
		cancel()
	}()

	logger.Printf("OfficeClaw is running. Minimized to system tray.")
	tray.Run(cfg, cancel, logger)
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

	// Initialize tool registry with tools that don't require WhatsApp
	toolRegistry := tools.NewRegistry()

	if cfg.Tools.FileAccess.Enabled {
		toolRegistry.Register(tools.NewFileAccessTool(cfg.Tools.FileAccess))
	}
	if cfg.Tools.TaskExecution.Enabled {
		// In MCP mode, no WhatsApp client available, so pass nil
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

	// Note: send_message tool requires WhatsApp client which isn't available in standalone mode
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
