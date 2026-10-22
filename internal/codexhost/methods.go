package codexhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// InitializeResult is the subset of v1/InitializeResponse AgentDock consumes.
type InitializeResult struct {
	UserAgent      string `json:"userAgent"`
	CodexHome      string `json:"codexHome"`
	PlatformFamily string `json:"platformFamily"`
	PlatformOS     string `json:"platformOs"`
}

// Initialize performs the app-server handshake. Codex treats `initialized` as a
// separate notification; sending it is mandatory before thread methods work.
func (c *Client) Initialize(ctx context.Context) (InitializeResult, error) {
	params := map[string]any{
		"clientInfo": map[string]any{
			"name":    c.clientName(),
			"title":   "AgentDock",
			"version": c.clientVersion(),
		},
	}
	raw, err := c.transport.call(ctx, "initialize", params)
	if err != nil {
		return InitializeResult{}, wrapCall("initialize Codex app-server", err)
	}
	var result InitializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return InitializeResult{}, newError(CodeInvalidResponse, "decode Codex initialize response", false, nil, err)
	}
	if result.CodexHome == "" || result.UserAgent == "" {
		return InitializeResult{}, newError(CodeInvalidResponse, "Codex initialize response is incomplete", false, map[string]any{"codex_home": result.CodexHome, "user_agent": result.UserAgent}, nil)
	}
	if err := c.transport.notify("initialized", nil); err != nil {
		return InitializeResult{}, wrapCall("notify Codex app-server initialized", err)
	}
	c.mu.Lock()
	c.info = result
	c.mu.Unlock()
	return result, nil
}

// Info returns the handshake result; the zero value means the client has not
// completed initialize.
func (c *Client) Info() InitializeResult {
	if c == nil {
		return InitializeResult{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.info
}

// ThreadStartParams carries the subset of v2/ThreadStartParams AgentDock sets.
type ThreadStartParams struct {
	CWD         string
	Ephemeral   bool
	Sandbox     string
	Model       string
	ApprovalTag string
}

// StartThread creates the thread that owns MCP tool calls. Only `thread.id` is
// decoded: the full Thread object is large and AgentDock has no use for it.
func (c *Client) StartThread(ctx context.Context, params ThreadStartParams) (string, error) {
	request := map[string]any{"ephemeral": params.Ephemeral}
	if cwd := strings.TrimSpace(params.CWD); cwd != "" {
		request["cwd"] = cwd
	}
	if sandbox := strings.TrimSpace(params.Sandbox); sandbox != "" {
		request["sandbox"] = sandbox
	}
	if model := strings.TrimSpace(params.Model); model != "" {
		request["model"] = model
	}
	raw, err := c.transport.call(ctx, "thread/start", request)
	if err != nil {
		return "", wrapCall("start Codex thread", err)
	}
	var response struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return "", newError(CodeInvalidResponse, "decode Codex thread/start response", false, nil, err)
	}
	if response.Thread.ID == "" {
		return "", newError(CodeInvalidResponse, "Codex thread/start response omitted the thread id", false, nil, nil)
	}
	c.mu.Lock()
	c.threadID = response.Thread.ID
	c.mu.Unlock()
	return response.Thread.ID, nil
}

// ThreadID returns the active thread id, or "" before StartThread.
func (c *Client) ThreadID() string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.threadID
}

// MCPToolInfo is one tool advertised by an MCP server Codex connected.
type MCPToolInfo struct {
	Name        string          `json:"name"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// MCPServerStatus is one entry of mcpServerStatus/list, flattened for display.
type MCPServerStatus struct {
	Name          string                 `json:"name"`
	AuthStatus    string                 `json:"authStatus"`
	RuntimeStatus string                 `json:"runtimeStatus,omitempty"`
	ServerName    string                 `json:"serverName,omitempty"`
	ServerVersion string                 `json:"serverVersion,omitempty"`
	PluginID      string                 `json:"pluginId,omitempty"`
	HTTPOrigin    string                 `json:"httpOrigin,omitempty"`
	ToolsError    string                 `json:"toolsError,omitempty"`
	Tools         map[string]MCPToolInfo `json:"tools"`
}

// ListMCPServers enumerates the MCP servers Codex has for the thread, following
// nextCursor until Codex stops paginating.
//
// detail "toolsAndAuthOnly" keeps the response small; omitting detail requests
// the full catalogue including resources, which computer use does not need.
func (c *Client) ListMCPServers(ctx context.Context, threadID, detail string) ([]MCPServerStatus, error) {
	if strings.TrimSpace(threadID) == "" {
		return nil, newError(CodeInvalidArgument, "thread id is required to list MCP servers", false, nil, nil)
	}
	if strings.TrimSpace(detail) == "" {
		detail = "toolsAndAuthOnly"
	}
	var servers []MCPServerStatus
	cursor := ""
	for range mcpStatusMaxPages {
		params := map[string]any{"threadId": threadID, "detail": detail}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.transport.call(ctx, "mcpServerStatus/list", params)
		if err != nil {
			return nil, wrapCall("list Codex MCP servers", err)
		}
		var response struct {
			Data       []mcpServerStatusWire `json:"data"`
			NextCursor *string               `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &response); err != nil {
			return nil, newError(CodeInvalidResponse, "decode Codex mcpServerStatus/list response", false, nil, err)
		}
		for _, entry := range response.Data {
			servers = append(servers, entry.flatten())
		}
		if response.NextCursor == nil || strings.TrimSpace(*response.NextCursor) == "" {
			return servers, nil
		}
		cursor = *response.NextCursor
	}
	return servers, nil
}

// mcpStatusMaxPages bounds pagination so a misbehaving host cannot spin forever.
const mcpStatusMaxPages = 50

type mcpServerStatusWire struct {
	Name          string `json:"name"`
	AuthStatus    string `json:"authStatus"`
	RuntimeStatus string `json:"runtimeStatus"`
	PluginID      string `json:"pluginId"`
	HTTPOrigin    string `json:"httpOrigin"`
	ToolsError    string `json:"toolsError"`
	ServerInfo    *struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
	Tools map[string]struct {
		Name        string          `json:"name"`
		Title       string          `json:"title"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"inputSchema"`
	} `json:"tools"`
}

func (w mcpServerStatusWire) flatten() MCPServerStatus {
	status := MCPServerStatus{
		Name:          w.Name,
		AuthStatus:    w.AuthStatus,
		RuntimeStatus: w.RuntimeStatus,
		PluginID:      w.PluginID,
		HTTPOrigin:    w.HTTPOrigin,
		ToolsError:    w.ToolsError,
		Tools:         make(map[string]MCPToolInfo, len(w.Tools)),
	}
	if w.ServerInfo != nil {
		status.ServerName = w.ServerInfo.Name
		status.ServerVersion = w.ServerInfo.Version
	}
	for key, tool := range w.Tools {
		name := tool.Name
		if name == "" {
			name = key
		}
		status.Tools[key] = MCPToolInfo{
			Name:        name,
			Title:       tool.Title,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
		}
	}
	return status
}

// ToolCallResult preserves the MCP CallToolResult shape verbatim. Content items
// stay raw JSON because computer use returns text plus inline images, and
// AgentDock must forward them without re-encoding.
type ToolCallResult struct {
	Content           []json.RawMessage `json:"content"`
	IsError           bool              `json:"isError,omitempty"`
	StructuredContent json.RawMessage   `json:"structuredContent,omitempty"`
}

// CallMCPTool proxies one MCP tool invocation through the Codex host.
//
// The `server` and `tool` arguments are separate here; Codex's own MCP client
// names tools `<server>:<tool>`, and that conversion belongs to the caller.
func (c *Client) CallMCPTool(ctx context.Context, server, threadID, tool string, arguments map[string]any) (*ToolCallResult, error) {
	server = strings.TrimSpace(server)
	tool = strings.TrimSpace(tool)
	if server == "" || tool == "" {
		return nil, newError(CodeInvalidArgument, "server and tool are required", false, map[string]any{"server": server, "tool": tool}, nil)
	}
	if strings.TrimSpace(threadID) == "" {
		return nil, newError(CodeInvalidArgument, "thread id is required to call an MCP tool", false, nil, nil)
	}
	if arguments == nil {
		arguments = map[string]any{}
	}
	params := map[string]any{"server": server, "threadId": threadID, "tool": tool, "arguments": arguments}
	raw, err := c.transport.call(ctx, "mcpServer/tool/call", params)
	if err != nil {
		return nil, wrapCall("call Codex MCP tool "+server+"/"+tool, err)
	}
	var response struct {
		Content           []json.RawMessage `json:"content"`
		IsError           *bool             `json:"isError"`
		StructuredContent json.RawMessage   `json:"structuredContent"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, newError(CodeInvalidResponse, "decode Codex mcpServer/tool/call response", false, nil, err)
	}
	if response.IsError == nil && len(response.Content) == 0 && len(response.StructuredContent) == 0 {
		return nil, newError(CodeInvalidResponse, "Codex mcpServer/tool/call response carried no content", false, nil, nil)
	}
	result := &ToolCallResult{
		Content:           response.Content,
		StructuredContent: response.StructuredContent,
	}
	if response.IsError != nil {
		result.IsError = *response.IsError
	}
	return result, nil
}

func wrapCall(operation string, err error) error {
	var hostErr *Error
	if errors.As(err, &hostErr) {
		return hostErr
	}
	return newError(CodeConnectionFailed, operation, true, nil, err)
}

// Text returns the concatenated text content of a tool result, which is the
// only content form computer use produces besides inline images.
func (r *ToolCallResult) Text() string {
	if r == nil {
		return ""
	}
	var builder strings.Builder
	for _, item := range r.Content {
		var block struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(item, &block) != nil || block.Type != "text" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString(block.Text)
	}
	return builder.String()
}

// Images returns the base64 image payloads carried by a tool result.
func (r *ToolCallResult) Images() []ImageContent {
	if r == nil {
		return nil
	}
	images := make([]ImageContent, 0, 1)
	for _, item := range r.Content {
		var block ImageContent
		if json.Unmarshal(item, &block) != nil || block.Type != "image" || block.Data == "" {
			continue
		}
		if block.MimeType == "" {
			block.MimeType = "image/png"
		}
		images = append(images, block)
	}
	return images
}

// ImageContent is one MCP image content block.
type ImageContent struct {
	Type     string `json:"type"`
	Data     string `json:"data"`
	MimeType string `json:"mimeType,omitempty"`
}

func (c *Client) clientName() string {
	if c == nil || strings.TrimSpace(c.clientInfo.Name) == "" {
		return "agentdock"
	}
	return c.clientInfo.Name
}

func (c *Client) clientVersion() string {
	if c == nil || strings.TrimSpace(c.clientInfo.Version) == "" {
		return "0.0.0"
	}
	return c.clientInfo.Version
}

func (r InitializeResult) String() string {
	return fmt.Sprintf("%s (%s/%s, codex home %s)", r.UserAgent, r.PlatformFamily, r.PlatformOS, r.CodexHome)
}
