package computer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/uvwt/agentdock/internal/codexhost"
	toolcore "github.com/uvwt/agentdock/internal/tool/core"
)

type fakeHost struct {
	servers   []codexhost.MCPServerStatus
	result    *codexhost.ToolCallResult
	err       error
	closed    bool
	codexHome string

	lastServer    string
	lastTool      string
	lastArguments map[string]any
	lastThread    string
}

func (f *fakeHost) Info() codexhost.InitializeResult {
	return codexhost.InitializeResult{CodexHome: f.codexHome}
}

func (f *fakeHost) EnsureThread(context.Context, string) (string, error) {
	return "thread-1", nil
}

func (f *fakeHost) ListMCPServers(context.Context, string, string) ([]codexhost.MCPServerStatus, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.servers, nil
}

func (f *fakeHost) CallMCPTool(_ context.Context, server, threadID, tool string, arguments map[string]any) (*codexhost.ToolCallResult, error) {
	f.lastServer, f.lastThread, f.lastTool, f.lastArguments = server, threadID, tool, arguments
	if f.err != nil {
		return nil, f.err
	}
	f.lastThread = threadID
	return f.result, nil
}

func (f *fakeHost) Close() error {
	f.closed = true
	return nil
}

// startCounter records how many times the service spawned a host process, so
// tests can prove a dead child is discarded instead of being reused forever.
type startCounter struct {
	hosts []*fakeHost
	calls int
}

func (c *startCounter) start(context.Context, codexhost.Config) (hostClient, error) {
	c.calls++
	if c.calls <= len(c.hosts) {
		return c.hosts[c.calls-1], nil
	}
	return c.hosts[len(c.hosts)-1], nil
}

func newTestService(host *fakeHost) *Service {
	service := New(Config{})
	service.start = func(context.Context, codexhost.Config) (hostClient, error) {
		return host, nil
	}
	return service
}

// TestHandleCallPassesContentThrough proves AgentDock forwards the plugin's
// content blocks verbatim. Computer use answers with a text accessibility tree
// plus inline images; reshaping either would break parity with the official
// plugin and with the Codex UI.
func TestHandleCallPassesContentThrough(t *testing.T) {
	host := &fakeHost{result: &codexhost.ToolCallResult{
		Content: []json.RawMessage{
			json.RawMessage(`{"type":"text","text":"App=com.apple.finder (pid 1106)\n    0 standard window"}`),
			json.RawMessage(`{"type":"image","data":"iVBORw0KGgo=","mimeType":"image/png"}`),
		},
		StructuredContent: json.RawMessage(`{"window_id":7}`),
	}}
	service := newTestService(host)
	defer func() { _ = service.Close() }()

	result, err := service.HandleCall(context.Background(), map[string]any{
		"tool":      "get_app_state",
		"arguments": map[string]any{"app": "Finder"},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertMatchesOutputSchema(t, ToolCall, result)

	if result["server"] != DefaultServerName {
		t.Fatalf("server default not applied: %v", result["server"])
	}
	if result["is_error"] != false {
		t.Fatalf("unexpected is_error: %v", result["is_error"])
	}
	content, _ := result["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("expected 2 passthrough blocks, got %d", len(content))
	}
	image, _ := content[1].(map[string]any)
	if image["type"] != "image" || image["data"] != "iVBORw0KGgo=" {
		t.Fatalf("image block was altered: %v", image)
	}
	if host.lastTool != "get_app_state" || host.lastServer != DefaultServerName || host.lastThread != "thread-1" {
		t.Fatalf("unexpected routing: server=%q tool=%q thread=%q", host.lastServer, host.lastTool, host.lastThread)
	}
	if host.lastArguments["app"] != "Finder" {
		t.Fatalf("arguments were not forwarded: %v", host.lastArguments)
	}
	if text, _ := result["text"].(string); text != "App=com.apple.finder (pid 1106)\n    0 standard window" {
		t.Fatalf("text projection wrong: %q", text)
	}
}

// TestHandleCallReportsPluginError proves a tool-level failure stays a tool
// result (is_error) rather than becoming a proxy error: the official plugin
// reports failures as content text plus isError.
func TestHandleCallReportsPluginError(t *testing.T) {
	host := &fakeHost{result: &codexhost.ToolCallResult{
		Content: []json.RawMessage{json.RawMessage(`{"type":"text","text":"appNotFound(\"iTerm2\")"}`)},
		IsError: true,
	}}
	service := newTestService(host)

	result, err := service.HandleCall(context.Background(), map[string]any{"tool": "get_app_state"})
	if err != nil {
		t.Fatal(err)
	}
	if result["host_ok"] != true || result["is_error"] != true {
		t.Fatalf("unexpected result: %v", result)
	}
	if _, hasError := result["error"]; hasError {
		t.Fatalf("plugin-level failure must not set the proxy error field: %v", result)
	}
	assertMatchesOutputSchema(t, ToolCall, result)
}

// TestHandleCallHostFailureCarriesErrorCode proves transport failures produce a
// structured AgentDock error instead of an MCP-level exception, so a host without
// Codex installed simply reports an unavailable capability.
func TestHandleCallHostFailureCarriesErrorCode(t *testing.T) {
	host := &fakeHost{err: &codexhost.Error{Code: codexhost.CodeCodexNotFound, Message: "Codex CLI is not installed"}}
	service := newTestService(host)

	result, err := service.HandleCall(context.Background(), map[string]any{"tool": "list_apps"})
	if err != nil {
		t.Fatal(err)
	}
	if result["host_ok"] != false || result["is_error"] != true {
		t.Fatalf("unexpected result: %v", result)
	}
	payload, _ := result["error"].(map[string]any)
	if payload["code"] != ErrHostUnavailable {
		t.Fatalf("expected %s, got %v", ErrHostUnavailable, payload["code"])
	}
	details, _ := payload["details"].(map[string]any)
	if details["codex_code"] != codexhost.CodeCodexNotFound {
		t.Fatalf("expected codex_code to be preserved, got %v", details)
	}
	assertMatchesOutputSchema(t, ToolCall, result)
}

// TestHandleStatusFilterDoesNotHideAvailability proves the `server` argument is
// a display filter only: asking about an unrelated server must still report the
// computer-use plugin as available, because availability is a host property.
func TestHandleStatusFilterDoesNotHideAvailability(t *testing.T) {
	host := &fakeHost{servers: []codexhost.MCPServerStatus{
		{Name: DefaultServerName, RuntimeStatus: "connected", Tools: map[string]codexhost.MCPToolInfo{
			"list_apps": {Name: "list_apps"},
		}},
	}}
	service := newTestService(host)

	result, err := service.HandleStatus(context.Background(), map[string]any{"server": "codex_apps"})
	if err != nil {
		t.Fatal(err)
	}
	if servers, _ := result["servers"].([]map[string]any); len(servers) != 0 {
		t.Fatalf("filter should hide unrelated servers: %v", servers)
	}
	computerUse, _ := result["computer_use"].(map[string]any)
	if computerUse["available"] != true {
		t.Fatalf("filter must not change availability: %v", computerUse)
	}
	assertMatchesOutputSchema(t, ToolStatus, result)
}

func TestHandleCallRejectsMissingTool(t *testing.T) {
	service := newTestService(&fakeHost{})
	_, err := service.HandleCall(context.Background(), map[string]any{})
	var toolErr *toolcore.ToolError
	if !errors.As(err, &toolErr) || toolErr.Code != ErrInvalidArguments {
		t.Fatalf("expected %s, got %v", ErrInvalidArguments, err)
	}
}

// TestHandleStatusDetectsComputerUse proves the status tool reports whether the
// host actually exposes the computer-use plugin, which is the only reliable
// installation check: the plugin ships inside the signed desktop apps and is
// absent from the Linux CLI build.
// TestHandleCallRejectsNonObjectArguments proves a malformed arguments value is
// reported as a caller error instead of being silently coerced to {} and sent to
// the plugin, which would answer with a misleading schema-validation message.
func TestHandleCallRejectsNonObjectArguments(t *testing.T) {
	host := &fakeHost{result: &codexhost.ToolCallResult{}}
	_, err := newTestService(host).HandleCall(context.Background(), map[string]any{
		"tool":      "list_apps",
		"arguments": "not-an-object",
	})
	if err == nil {
		t.Fatal("expected invalid arguments error")
	}
	var toolErr *toolcore.ToolError
	if !errors.As(err, &toolErr) || toolErr.Code != ErrInvalidArguments {
		t.Fatalf("error = %#v", err)
	}
	if host.lastTool != "" {
		t.Fatal("the host must not be called with malformed arguments")
	}
}

func TestHandleStatusDetectsComputerUse(t *testing.T) {
	host := &fakeHost{servers: []codexhost.MCPServerStatus{
		{Name: "codex_apps", AuthStatus: "bearerToken", RuntimeStatus: "connected"},
		{Name: DefaultServerName, AuthStatus: "unsupported", RuntimeStatus: "connected", Tools: map[string]codexhost.MCPToolInfo{
			"list_apps":     {Name: "list_apps", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)},
			"get_app_state": {Name: "get_app_state"},
			"click":         {Name: "click"},
		}},
	}}
	service := newTestService(host)

	result, err := service.HandleStatus(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	assertMatchesOutputSchema(t, ToolStatus, result)

	detected, _ := result["computer_use"].(map[string]any)
	if detected["available"] != true {
		t.Fatalf("computer-use was not detected: %v", detected)
	}
	tools, _ := detected["tools"].([]string)
	if len(tools) != 3 {
		t.Fatalf("expected 3 tools, got %v", detected["tools"])
	}
	servers, _ := result["servers"].([]map[string]any)
	if len(servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(servers))
	}
}

// TestHandleStatusReportsResolvedCodexHome proves discovery surfaces the
// CODEX_HOME the child resolved: a wrong directory looks exactly like a missing
// plugin, and it is the first thing an operator has to compare with a shell
// that does have the plugin.
func TestHandleStatusReportsResolvedCodexHome(t *testing.T) {
	host := &fakeHost{
		codexHome: "/home/user/.codex-work",
		servers:   []codexhost.MCPServerStatus{{Name: "codex_apps"}},
	}

	result, err := newTestService(host).HandleStatus(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if result["codex_home"] != "/home/user/.codex-work" {
		t.Fatalf("codex_home = %#v", result["codex_home"])
	}
	assertMatchesOutputSchema(t, ToolStatus, result)
}

// A host that never completed the handshake reports no CODEX_HOME; the field
// stays absent instead of leaking an empty string into the discovery payload.
func TestHandleStatusOmitsUnknownCodexHome(t *testing.T) {
	result, err := newTestService(&fakeHost{servers: []codexhost.MCPServerStatus{{Name: "codex_apps"}}}).
		HandleStatus(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if _, present := result["codex_home"]; present {
		t.Fatalf("codex_home should be omitted, got %#v", result["codex_home"])
	}
}

func TestHandleStatusReportsMissingPlugin(t *testing.T) {
	host := &fakeHost{servers: []codexhost.MCPServerStatus{{Name: "codex_apps", AuthStatus: "bearerToken"}}}
	service := newTestService(host)

	result, err := service.HandleStatus(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	detected, _ := result["computer_use"].(map[string]any)
	if detected["available"] != false {
		t.Fatalf("expected unavailable, got %v", detected)
	}
	if detected["reason"] == "" {
		t.Fatalf("expected a reason, got %v", detected)
	}
	assertMatchesOutputSchema(t, ToolStatus, result)
}

// TestHandleCallRespawnsHostAfterConnectionLoss proves a dead child process is
// dropped: the transport error is reported once, and the next call starts a
// fresh host instead of retrying a closed connection forever.
func TestHandleCallRespawnsHostAfterConnectionLoss(t *testing.T) {
	dead := &fakeHost{err: &codexhost.Error{Code: codexhost.CodeConnectionClosed, Message: "stream reached EOF"}}
	live := &fakeHost{result: &codexhost.ToolCallResult{
		Content: []json.RawMessage{json.RawMessage(`{"type":"text","text":"ok"}`)},
	}}
	counter := &startCounter{hosts: []*fakeHost{dead, live}}
	service := New(Config{})
	service.start = counter.start

	failed, err := service.HandleCall(context.Background(), map[string]any{"tool": "list_apps"})
	if err != nil {
		t.Fatal(err)
	}
	if failed["host_ok"] != false {
		t.Fatalf("first call should fail: %v", failed)
	}
	if !dead.closed {
		t.Fatal("dead host was not closed")
	}

	recovered, err := service.HandleCall(context.Background(), map[string]any{"tool": "list_apps"})
	if err != nil {
		t.Fatal(err)
	}
	if recovered["host_ok"] != true {
		t.Fatalf("second call should use a fresh host: %v", recovered)
	}
	if counter.calls != 2 {
		t.Fatalf("expected 2 host starts, got %d", counter.calls)
	}
}

// TestHandleCallKeepsHostOnPluginFailure proves a plugin-level (or timeout)
// failure does not tear down a healthy child: only terminal connection errors
// trigger a respawn.
func TestHandleCallKeepsHostOnPluginFailure(t *testing.T) {
	host := &fakeHost{err: &codexhost.Error{Code: codexhost.CodeRemoteError, Message: "tool failed"}}
	counter := &startCounter{hosts: []*fakeHost{host}}
	service := New(Config{})
	service.start = counter.start

	for range 2 {
		if _, err := service.HandleCall(context.Background(), map[string]any{"tool": "list_apps"}); err != nil {
			t.Fatal(err)
		}
	}
	if host.closed {
		t.Fatal("healthy host must not be closed on a plugin-level failure")
	}
	if counter.calls != 1 {
		t.Fatalf("expected 1 host start, got %d", counter.calls)
	}
}

// TestResetIfDeadIgnoresReplacedClient proves a late terminal failure from a
// client that another goroutine already replaced cannot tear down the live one:
// concurrent calls share the service, so a failure observed on an older client
// must only close that client.
func TestResetIfDeadIgnoresReplacedClient(t *testing.T) {
	current := &fakeHost{}
	stale := &fakeHost{}
	service := New(Config{})
	service.client = current
	service.threadID = "thread-1"

	service.resetIfDead(stale, &codexhost.Error{Code: codexhost.CodeConnectionClosed, Message: "closed"})
	if service.client != current {
		t.Fatal("a stale failure must not drop the replacement client")
	}
	if stale.closed {
		t.Fatal("the stale client is not owned by the service any more")
	}

	service.resetIfDead(current, &codexhost.Error{Code: codexhost.CodeConnectionClosed, Message: "closed"})
	if service.client != nil || !current.closed {
		t.Fatal("a terminal failure from the cached client must drop and close it")
	}
}

// TestHandleStatusKeepsShapeWhenHostUnavailable proves the failure path still
// carries computer_use.available, so a UI can render "unknown/unavailable"
// instead of crashing on a missing key.
func TestHandleStatusKeepsShapeWhenHostUnavailable(t *testing.T) {
	host := &fakeHost{err: &codexhost.Error{Code: codexhost.CodeNotRunning, Message: "not running"}}
	result, err := newTestService(host).HandleStatus(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if result["host_ok"] != false {
		t.Fatalf("host_ok = %#v", result["host_ok"])
	}
	failure, ok := result["computer_use"].(map[string]any)
	if !ok {
		t.Fatalf("computer_use missing on failure path: %#v", result["computer_use"])
	}
	if failure["available"] != false || failure["server"] != DefaultServerName || failure["reason"] == "" {
		t.Fatalf("computer_use = %#v", failure)
	}
	if _, ok := result["error"].(map[string]any); !ok {
		t.Fatalf("error = %#v", result["error"])
	}
	assertMatchesOutputSchema(t, ToolStatus, result)
}

func TestCloseClosesHostOnce(t *testing.T) {
	host := &fakeHost{}
	service := newTestService(host)
	if _, _, err := service.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if !host.closed {
		t.Fatal("host was not closed")
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
}

func assertMatchesOutputSchema(t *testing.T, name string, result toolcore.Result) {
	t.Helper()
	schema, ok := OutputSchema(name)
	if !ok {
		t.Fatalf("%s has no output schema", name)
	}
	schemaJSON, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	var compiled jsonschema.Schema
	if err := json.Unmarshal(schemaJSON, &compiled); err != nil {
		t.Fatal(err)
	}
	resolved, err := compiled.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var instance any
	if err := json.Unmarshal(resultJSON, &instance); err != nil {
		t.Fatal(err)
	}
	if err := resolved.Validate(instance); err != nil {
		t.Fatalf("%s result violates output schema: %v\nresult: %s", name, err, resultJSON)
	}
}
