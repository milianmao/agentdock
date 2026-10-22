package app

import (
	"context"

	toolcomputer "github.com/uvwt/agentdock/internal/tool/computer"
)

func computerToolSpecs() []ToolSpec {
	return []ToolSpec{
		{Name: toolcomputer.ToolStatus, Contract: computerToolContract, Title: "Computer status", Description: "List the MCP servers the Codex host exposes to AgentDock, including whether the computer-use plugin is installed and which tools it advertises. Use this before computer_call to discover the real tool names and input schemas on this machine.", Annotations: readOnlyToolAnnotations(false), Availability: requiresComputer, Handler: func(ctx context.Context, r *Runtime, args map[string]any) (Result, error) {
			return r.computer.HandleStatus(ctx, args)
		}},
		{Name: toolcomputer.ToolCall, Contract: computerToolContract, Title: "Computer call", Description: "Proxy one Computer Use tool call through the Codex host and return the plugin's content blocks unchanged: a text accessibility tree plus inline screenshots. AgentDock does not reinterpret desktop state, so element indices and result semantics are exactly those documented by the Codex computer-use plugin.", Annotations: mutatingToolAnnotations(true, true), Availability: requiresComputer, Handler: func(ctx context.Context, r *Runtime, args map[string]any) (Result, error) {
			return r.computer.HandleCall(ctx, args)
		}},
	}
}
