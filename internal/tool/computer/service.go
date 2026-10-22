package computer

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/uvwt/agentdock/internal/codexhost"
	toolcore "github.com/uvwt/agentdock/internal/tool/core"
)

// Config configures the Codex-hosted Computer Use proxy.
type Config struct {
	// ExecutablePath/CodexHome are passed through to codexhost discovery.
	ExecutablePath string
	CodexHome      string
	// ServerName is the MCP server name to proxy calls to.
	ServerName string
	// CWD is the app-server working directory for its ephemeral thread.
	CWD string
}

// hostClient is the slice of codexhost.Client this service depends on. It keeps
// the proxy testable without spawning a real Codex CLI.
type hostClient interface {
	Info() codexhost.InitializeResult
	EnsureThread(ctx context.Context, cwd string) (string, error)
	ListMCPServers(ctx context.Context, threadID, detail string) ([]codexhost.MCPServerStatus, error)
	CallMCPTool(ctx context.Context, server, threadID, tool string, arguments map[string]any) (*codexhost.ToolCallResult, error)
	Close() error
}

// Service proxies MCP tool calls to the computer-use plugin running inside a
// Codex app-server child process.
//
// AgentDock deliberately does not implement a desktop executor: on macOS the
// accessibility client must be launched by a signed OpenAI-signed parent, so the
// only supported integration is borrowing Codex as the host.
type Service struct {
	mu       sync.Mutex
	cfg      Config
	client   hostClient
	threadID string

	start func(context.Context, codexhost.Config) (hostClient, error)
}

// New builds a service. Startup is lazy: a missing Codex CLI must not fail
// AgentDock startup, it only makes the computer tools unavailable.
func New(cfg Config) *Service {
	if strings.TrimSpace(cfg.ServerName) == "" {
		cfg.ServerName = DefaultServerName
	}
	return &Service{cfg: cfg, start: func(ctx context.Context, hostCfg codexhost.Config) (hostClient, error) {
		return codexhost.Start(ctx, hostCfg)
	}}
}

// ServerName returns the configured MCP server name.
func (s *Service) ServerName() string {
	if s == nil {
		return DefaultServerName
	}
	return s.cfg.ServerName
}

// HandleStatus reports the MCP servers Codex exposes for this AgentDock process,
// which is the only reliable way to tell whether the computer-use plugin is
// installed on this machine and this platform.
func (s *Service) HandleStatus(ctx context.Context, args map[string]any) (toolcore.Result, error) {
	includeTools := toolcore.BoolArg(args, "include_tools", true)
	serverFilter := strings.TrimSpace(toolcore.StringArg(args, "server", ""))
	timeout := timeoutFromArgs(args)

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client, threadID, err := s.acquire(callCtx)
	if err != nil {
		return s.statusFailure(err), nil
	}
	// toolsAndAuthOnly already carries the tool inventory; the full detail level
	// additionally returns resources and resource templates, which computer use
	// never advertises.
	servers, err := client.ListMCPServers(callCtx, threadID, "toolsAndAuthOnly")
	if err != nil {
		s.resetIfDead(client, err)
		return s.statusFailure(hostError("list MCP servers", err)), nil
	}

	reported := make([]map[string]any, 0, len(servers))
	computerUse := map[string]any{
		"available": false,
		"server":    s.ServerName(),
		"reason":    "the Codex host does not expose a computer-use MCP server on this platform",
	}
	for _, server := range servers {
		if server.Name == s.ServerName() {
			// Availability is a property of the host, not of the caller's
			// display filter: filtering must never turn a present plugin into
			// an absent one.
			computerUse = map[string]any{
				"available":      true,
				"server":         server.Name,
				"runtime_status": server.RuntimeStatus,
				"auth_status":    server.AuthStatus,
			}
			if includeTools {
				computerUse["tools"] = toolNames(server.Tools)
			}
		}
		if serverFilter != "" && server.Name != serverFilter {
			continue
		}
		entry := map[string]any{
			"name":           server.Name,
			"auth_status":    server.AuthStatus,
			"runtime_status": server.RuntimeStatus,
		}
		if server.ServerName != "" {
			entry["server_name"] = server.ServerName
		}
		if server.ServerVersion != "" {
			entry["server_version"] = server.ServerVersion
		}
		if server.HTTPOrigin != "" {
			entry["http_origin"] = server.HTTPOrigin
		}
		if server.ToolsError != "" {
			entry["tools_error"] = server.ToolsError
		}
		if includeTools {
			tools := make([]map[string]any, 0, len(server.Tools))
			for name, tool := range server.Tools {
				item := map[string]any{"name": name}
				if tool.Title != "" {
					item["title"] = tool.Title
				}
				if len(tool.InputSchema) > 0 {
					var schema any
					if json.Unmarshal(tool.InputSchema, &schema) == nil {
						item["input_schema"] = schema
					}
				}
				tools = append(tools, item)
			}
			entry["tools"] = tools
		}
		reported = append(reported, entry)
	}
	result := toolcore.Result{
		"host_ok":      true,
		"servers":      reported,
		"computer_use": computerUse,
	}
	// The handshake reports which CODEX_HOME the child actually resolved. It is
	// the fastest way to explain "the plugin is missing here but present in my
	// shell", so it belongs in the discovery output rather than only in a log.
	if codexHome := client.Info().CodexHome; codexHome != "" {
		result["codex_home"] = codexHome
	}
	return result, nil
}

// HandleCall proxies one MCP tool call. Content blocks are passed through
// unchanged: computer use returns a text accessibility tree plus inline images,
// and re-encoding them into an AgentDock-specific shape would break parity with
// the official plugin.
func (s *Service) HandleCall(ctx context.Context, args map[string]any) (toolcore.Result, error) {
	toolName := strings.TrimSpace(toolcore.StringArg(args, "tool", ""))
	if toolName == "" {
		return nil, invalidArguments("tool is required")
	}
	serverName := strings.TrimSpace(toolcore.StringArg(args, "server", s.ServerName()))
	arguments, err := argumentsArg(args)
	if err != nil {
		return nil, err
	}
	timeout := timeoutFromArgs(args)

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client, threadID, err := s.acquire(callCtx)
	if err != nil {
		return callFailure(serverName, toolName, err), nil
	}
	result, err := client.CallMCPTool(callCtx, serverName, threadID, toolName, arguments)
	if err != nil {
		s.resetIfDead(client, err)
		return callFailure(serverName, toolName, hostError("call MCP tool", err)), nil
	}
	return toolResult(serverName, toolName, result), nil
}

// argumentsArg normalises the free-form tool arguments. A non-object value is a
// caller bug worth reporting instead of silently forwarding {} to the plugin:
// the plugin would answer with a confusing schema error instead of the actual
// problem.
func argumentsArg(args map[string]any) (map[string]any, error) {
	value, ok := args["arguments"]
	if !ok || value == nil {
		return map[string]any{}, nil
	}
	arguments, ok := value.(map[string]any)
	if !ok {
		return nil, invalidArguments("arguments must be a JSON object")
	}
	return arguments, nil
}

// isTerminalHostError reports whether the host connection can no longer be
// used, so the cached client must be discarded. A canceled request or a
// plugin-level failure leaves the child process healthy.
func isTerminalHostError(err error) bool {
	switch codexhost.ErrorCode(err) {
	case codexhost.CodeConnectionClosed, codexhost.CodeConnectionFailed, codexhost.CodeNotRunning:
		return true
	default:
		return false
	}
}

// resetIfDead drops the cached client when the host process is gone, so the
// next call respawns Codex instead of retrying a dead transport forever. Only
// terminal connection states trigger a reset: a single timed-out request must
// not throw away a healthy child process.
func (s *Service) resetIfDead(client hostClient, err error) {
	if !isTerminalHostError(err) {
		return
	}
	s.mu.Lock()
	// Identity check: a concurrent call may already have replaced the dead
	// client with a fresh one, which must not be torn down by a late failure.
	if s.client != client {
		s.mu.Unlock()
		return
	}
	s.client = nil
	s.threadID = ""
	s.mu.Unlock()
	if client != nil {
		_ = client.Close()
	}
}

// acquire returns a started client and its thread id, starting both on demand.
// The mutex covers the whole startup so concurrent first calls cannot spawn two
// Codex children; once connected, callers share the transport concurrently.
func (s *Service) acquire(ctx context.Context) (hostClient, string, error) {
	stale, client, threadID, err := s.connect(ctx)
	if stale != nil {
		_ = stale.Close()
	}
	return client, threadID, err
}

// connect returns a live client. A client that turned out to be unreachable is
// returned as `stale` so the caller closes it outside the lock.
func (s *Service) connect(ctx context.Context) (stale hostClient, client hostClient, threadID string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.client != nil {
		if s.threadID != "" {
			return nil, s.client, s.threadID, nil
		}
		threadID, threadErr := s.client.EnsureThread(ctx, s.cfg.CWD)
		if threadErr != nil {
			if isTerminalHostError(threadErr) {
				stale, s.client, s.threadID = s.client, nil, ""
			}
			return stale, nil, "", hostError("start Codex thread", threadErr)
		}
		s.threadID = threadID
		return nil, s.client, s.threadID, nil
	}

	client, startErr := s.start(ctx, codexhost.Config{
		ExecutablePath: s.cfg.ExecutablePath,
		CodexHome:      s.cfg.CodexHome,
		CWD:            s.cfg.CWD,
	})
	if startErr != nil {
		return nil, nil, "", hostError("start Codex app-server", startErr)
	}
	threadID, threadErr := client.EnsureThread(ctx, s.cfg.CWD)
	if threadErr != nil {
		_ = client.Close()
		return nil, nil, "", hostError("start Codex thread", threadErr)
	}
	s.client = client
	s.threadID = threadID
	return nil, client, threadID, nil
}

// Close stops the Codex child process. It is safe on a never-started service.
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	client := s.client
	s.client = nil
	s.threadID = ""
	s.mu.Unlock()
	if client == nil {
		return nil
	}
	return client.Close()
}

func toolResult(server, tool string, result *codexhost.ToolCallResult) toolcore.Result {
	content := make([]any, 0, len(result.Content))
	for _, item := range result.Content {
		var block any
		if json.Unmarshal(item, &block) == nil {
			content = append(content, block)
		}
	}
	payload := toolcore.Result{
		"host_ok":  true,
		"server":   server,
		"tool":     tool,
		"is_error": result.IsError,
		"content":  content,
		"text":     result.Text(),
	}
	if len(result.StructuredContent) > 0 && string(result.StructuredContent) != "null" {
		var structured any
		if json.Unmarshal(result.StructuredContent, &structured) == nil {
			payload["structured"] = structured
		}
	}
	return payload
}

// statusFailure keeps the result shape identical to a successful discovery so
// callers can always read computer_use.available: when the host cannot be
// reached, availability is unknown and therefore reported as false with the
// reason rather than being omitted.
func (s *Service) statusFailure(err error) toolcore.Result {
	return toolcore.Result{
		"host_ok": false,
		"servers": []map[string]any{},
		"computer_use": map[string]any{
			"available": false,
			"server":    s.ServerName(),
			"reason":    "the Codex host is unreachable, so its MCP server inventory is unknown",
		},
		"error": toolErrorValue(err),
	}
}

func callFailure(server, tool string, err error) toolcore.Result {
	return toolcore.Result{
		"host_ok":  false,
		"server":   server,
		"tool":     tool,
		"is_error": true,
		"content":  []any{},
		"text":     "",
		"error":    toolErrorValue(err),
	}
}

func toolErrorValue(err error) map[string]any {
	var toolErr *toolcore.ToolError
	if !errors.As(err, &toolErr) {
		return map[string]any{"code": ErrCallFailed, "message": err.Error(), "category": "runtime"}
	}
	value := map[string]any{"code": toolErr.Code, "message": toolErr.Message, "category": toolErr.Category}
	if len(toolErr.Details) > 0 {
		value["details"] = toolErr.Details
	}
	return value
}

func toolNames(tools map[string]codexhost.MCPToolInfo) []string {
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	return names
}

func timeoutFromArgs(args map[string]any) time.Duration {
	milliseconds := toolcore.IntArg(args, "timeout_ms", defaultTimeoutMS)
	if milliseconds < 1 {
		milliseconds = defaultTimeoutMS
	}
	if milliseconds > maxTimeoutMS {
		milliseconds = maxTimeoutMS
	}
	return time.Duration(milliseconds) * time.Millisecond
}
