package test

import (
	"testing"

	"github.com/officeclaw/src/telegram"
)

func TestParseMachineTarget(t *testing.T) {
	tests := []struct {
		name              string
		input             string
		expectedTargets   []string
		expectedRemaining string
	}{
		{
			name:              "single target with message",
			input:             "@home refactor main.go",
			expectedTargets:   []string{"home"},
			expectedRemaining: "refactor main.go",
		},
		{
			name:              "multiple targets with message",
			input:             "@home,office check disk",
			expectedTargets:   []string{"home", "office"},
			expectedRemaining: "check disk",
		},
		{
			name:              "uppercase target",
			input:             "@HOME refactor",
			expectedTargets:   []string{"HOME"},
			expectedRemaining: "refactor",
		},
		{
			name:              "target only no message",
			input:             "@home",
			expectedTargets:   []string{"home"},
			expectedRemaining: "",
		},
		{
			name:              "no targeting",
			input:             "hello world",
			expectedTargets:   nil,
			expectedRemaining: "hello world",
		},
		{
			name:              "bare @ with space is untargeted",
			input:             "@ hello",
			expectedTargets:   nil,
			expectedRemaining: "@ hello",
		},
		{
			name:              "empty string",
			input:             "",
			expectedTargets:   nil,
			expectedRemaining: "",
		},
		{
			name:              "three targets",
			input:             "@home,office,lab do stuff",
			expectedTargets:   []string{"home", "office", "lab"},
			expectedRemaining: "do stuff",
		},
		{
			name:              "target with extra whitespace in message",
			input:             "@home   lots of space",
			expectedTargets:   []string{"home"},
			expectedRemaining: "lots of space",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			targets, remaining := telegram.ParseMachineTarget(tt.input)

			// Check targets
			if tt.expectedTargets == nil {
				if targets != nil {
					t.Errorf("expected nil targets, got %v", targets)
				}
			} else {
				if len(targets) != len(tt.expectedTargets) {
					t.Fatalf("expected %d targets, got %d: %v", len(tt.expectedTargets), len(targets), targets)
				}
				for i, expected := range tt.expectedTargets {
					if targets[i] != expected {
						t.Errorf("target[%d]: expected %q, got %q", i, expected, targets[i])
					}
				}
			}

			// Check remaining
			if remaining != tt.expectedRemaining {
				t.Errorf("expected remaining %q, got %q", tt.expectedRemaining, remaining)
			}
		})
	}
}
