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
	"github.com/officeclaw/src/tasks"
	"github.com/officeclaw/src/telemetry"
	"github.com/officeclaw/src/tools"
	"github.com/officeclaw/src/tray"
	"github.com/officeclaw/src/whatsapp"
)

func main() {
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

	// Register built-in tools
	if cfg.Tools.Messaging.Enabled {
		toolRegistry.Register(tools.NewMessagingTool(waClient))
	}
	if cfg.Tools.FileAccess.Enabled {
		toolRegistry.Register(tools.NewFileAccessTool(cfg.Tools.FileAccess))
	}
	if cfg.Tools.TaskExecution.Enabled {
		toolRegistry.Register(tools.NewTaskExecutionTool(taskExecutor))
	}
	if cfg.Tools.VPN.Enabled {
		toolRegistry.Register(tools.NewVPNTool(cfg.Tools.VPN))
	}
	logger.Printf("Tool registry initialized (%d tools registered)", toolRegistry.Count())

	// Create the core agent
	agentInstance := agent.New(agent.Config{
		LLMClient:    llmClient,
		ToolRegistry: toolRegistry,
		TaskExecutor: taskExecutor,
		Logger:       logger,
		DefaultTask:  cfg.WhatsApp.DefaultTask,
	})

	// Setup context with cancellation for graceful shutdown
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Bridge WhatsApp messages to agent
	waClient.SetMessageHandler(func(ctx context.Context, msg whatsapp.IncomingMessage) {
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

	// Start task scheduler
	go taskExecutor.StartScheduler(ctx)

	// Start system tray (blocks on main thread on Windows)
	go func() {
		// Listen for shutdown signals
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		logger.Printf("Shutdown signal received")
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
