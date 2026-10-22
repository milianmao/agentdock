package computer

import toolcontract "github.com/uvwt/agentdock/internal/tool/contract"

const (
	// ToolStatus lists the MCP servers Codex exposes to AgentDock, including the
	// computer-use plugin's tool inventory and input schemas.
	ToolStatus = "computer_status"
	// ToolCall proxies one MCP tool call through the Codex host. It is a
	// passthrough: AgentDock never redefines the computer-use tool schemas,
	// because the host's advertised schema is the single source of truth.
	ToolCall = "computer_call"
)

// DefaultServerName is the MCP server name the official computer-use plugin
// registers. Callers may override it, but the default keeps prompts short.
const DefaultServerName = "computer-use"

const (
	defaultTimeoutMS = 120000
	maxTimeoutMS     = 600000
)

func InputSchema(name string) (map[string]any, bool) {
	props := map[string]any{}
	var required []string

	switch name {
	case ToolStatus:
		props["server"] = toolcontract.String("Optional MCP server name filter. Omit to list every server Codex exposes.")
		props["include_tools"] = toolcontract.Boolean("Include each server's tool inventory and input schemas. Defaults to true.")
		props["timeout_ms"] = toolcontract.BoundedInteger("Discovery timeout in milliseconds. Defaults to 120000 and is capped at 600000.", 1, maxTimeoutMS)
	case ToolCall:
		props["server"] = toolcontract.String("MCP server name. Defaults to " + DefaultServerName + ".")
		props["tool"] = toolcontract.String("Tool name exactly as advertised by computer_status (for example list_apps or get_app_state).")
		props["arguments"] = toolcontract.OpenObject("Tool arguments in the shape declared by the tool's inputSchema.")
		props["timeout_ms"] = toolcontract.BoundedInteger("Call timeout in milliseconds. Defaults to 120000 and is capped at 600000.", 1, maxTimeoutMS)
		required = []string{"tool"}
	default:
		return nil, false
	}
	return toolcontract.InputObject(props, required...), true
}

func OutputSchema(name string) (map[string]any, bool) {
	switch name {
	case ToolStatus:
		return toolcontract.OutputObject(map[string]any{
			"host_ok":      toolcontract.Boolean("Whether the Codex host reported its MCP server inventory. False means AgentDock could not talk to Codex, not that computer use refused."),
			"servers":      toolcontract.ObjectArray("MCP servers reported by the Codex app-server, with runtime status, auth status and tool inventory."),
			"computer_use": toolcontract.OpenObject("Detected computer-use server: available, server name, tool names, and why it is unavailable."),
			"codex_home":   toolcontract.String("CODEX_HOME the Codex host resolved during initialize. Compare it with the shell that has the plugin installed: a wrong directory looks exactly like a missing plugin."),
			"error": toolcontract.OpenObject("AgentDock internal error classification with code, message, category and details." +
				" Computer Use itself has no error taxonomy: tool failures are reported by content text plus is_error."),
		}, "host_ok"), true
	case ToolCall:
		return CallOutputSchema(), true
	default:
		return nil, false
	}
}

// CallOutputSchema is the shape of computer_call results. It is kept separate
// because passthrough content items are raw MCP content blocks.
func CallOutputSchema() map[string]any {
	props := map[string]any{
		"host_ok":    toolcontract.Boolean("Whether the Codex host executed the proxied call. Distinct from is_error, which reports the plugin's own tool-level failure."),
		"server":     toolcontract.String("MCP server that executed the call."),
		"tool":       toolcontract.String("Tool name that was called."),
		"is_error":   toolcontract.Boolean("True when the MCP server itself reported a tool-level failure."),
		"content":    toolcontract.ObjectArray("Raw MCP content blocks, passed through unchanged: text blocks carry the accessibility tree and image blocks carry screenshots."),
		"text":       toolcontract.String("Concatenated text blocks, for hosts that cannot render content blocks."),
		"structured": toolcontract.OpenObject("structuredContent returned by the MCP server, when present."),
		"error":      toolcontract.OpenObject("AgentDock internal error classification, present only when the proxy itself failed."),
	}
	return toolcontract.OutputObject(props, "host_ok")
}
