package test

import (
	"context"
	"log"
	"os"
	"testing"

	"github.com/officeclaw/src/config"
	"github.com/officeclaw/src/tasks"
)

func TestTaskRegistryAndExecutor(t *testing.T) {
	registry := tasks.NewRegistry()

	registry.Register("test_echo", config.Task{
		Description:    "Echo test",
		Command:        "echo hello",
		TimeoutSeconds: 5,
	})

	if registry.Count() != 1 {
		t.Errorf("Expected 1 task, got %d", registry.Count())
	}

	task, ok := registry.Get("test_echo")
	if !ok {
		t.Fatal("Expected to find 'test_echo' task")
	}
	if task.Description != "Echo test" {
		t.Errorf("Expected description 'Echo test', got '%s'", task.Description)
	}
}

func TestTaskExecution(t *testing.T) {
	registry := tasks.NewRegistry()
	registry.Register("echo_test", config.Task{
		Description:    "Echo test",
		Command:        "echo hello_world",
		TimeoutSeconds: 10,
	})

	logger := log.New(os.Stdout, "[test] ", log.LstdFlags)
	executor := tasks.NewExecutor(registry, logger)

	result, err := executor.Execute(context.Background(), "echo_test", nil)
	if err != nil {
		t.Fatalf("Task execution failed: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("Expected status 'success', got '%s'", result.Status)
	}
}

func TestTaskTimeout(t *testing.T) {
	registry := tasks.NewRegistry()
	registry.Register("slow_task", config.Task{
		Description:    "Slow task that should timeout",
		Command:        "powershell -Command Start-Sleep -Seconds 30",
		TimeoutSeconds: 1,
	})

	logger := log.New(os.Stdout, "[test] ", log.LstdFlags)
	executor := tasks.NewExecutor(registry, logger)

	result, err := executor.Execute(context.Background(), "slow_task", nil)
	if err != nil {
		t.Fatalf("Task execution should not error (timeout returns result): %v", err)
	}
	if result.Status != "timeout" {
		t.Errorf("Expected status 'timeout', got '%s'", result.Status)
	}
}

func TestTaskUnknown(t *testing.T) {
	registry := tasks.NewRegistry()
	logger := log.New(os.Stdout, "[test] ", log.LstdFlags)
	executor := tasks.NewExecutor(registry, logger)

	_, err := executor.Execute(context.Background(), "nonexistent", nil)
	if err == nil {
		t.Error("Expected error for unknown task")
	}
}

func TestLLMDrivenTask(t *testing.T) {
	registry := tasks.NewRegistry()
	registry.Register("assist", config.Task{
		Description:    "General assistance",
		TimeoutSeconds: 120,
	})

	logger := log.New(os.Stdout, "[test] ", log.LstdFlags)
	executor := tasks.NewExecutor(registry, logger)

	result, err := executor.Execute(context.Background(), "assist", nil)
	if err != nil {
		t.Fatalf("LLM-driven task execution failed: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("Expected status 'success', got '%s'", result.Status)
	}
}
