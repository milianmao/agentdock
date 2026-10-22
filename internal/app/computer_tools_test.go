package app

import (
	"testing"

	"github.com/uvwt/agentdock/internal/config"
)

// TestComputerToolsAreFeatureGated proves contract S1: the proxy tools only
// exist when the operator turned the Codex host on. Nothing here touches a real
// Codex child — construction is lazy, so a missing CLI cannot fail the runtime.
func TestComputerToolsAreFeatureGated(t *testing.T) {
	disabled := config.Config{AgentDockHome: t.TempDir(), AgentDockDefaultDir: t.TempDir()}
	if err := disabled.Normalize(); err != nil {
		t.Fatal(err)
	}
	disabledRuntime, err := NewRuntime(disabled)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range disabledRuntime.ToolNames() {
		if name == "computer_status" || name == "computer_call" {
			disabledRuntime.Close()
			t.Fatalf("computer tool %s exposed while CodexHostEnabled=false", name)
		}
	}
	if err := disabledRuntime.Close(); err != nil {
		t.Fatal(err)
	}

	enabled := config.Config{
		AgentDockHome: t.TempDir(), AgentDockDefaultDir: t.TempDir(),
		CodexHostEnabled: true,
	}
	if err := enabled.Normalize(); err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(enabled)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runtime.Close() }()

	found := 0
	for _, name := range runtime.ToolNames() {
		if name == "computer_status" || name == "computer_call" {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("expected both computer tools when enabled, got %d", found)
	}
}
