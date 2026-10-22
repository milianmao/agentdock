package codexhost

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestRealAppServerSmoke drives a host-installed Codex CLI through the full
// proxy path. It is opt-in because Codex is a host-level dependency, matching
// the AGENTDOCK_TEST_ACP_COMMAND convention used by the ACP adapter smoke test.
//
// The assertions are deliberately structural: the probe tool call is expected
// to fail MCP-side validation (isError), because that outcome proves the
// request reached a real MCP server and its response came back through the
// proxy. A transport failure would surface as a Go error instead.
func TestRealAppServerSmoke(t *testing.T) {
	if strings.TrimSpace(os.Getenv("AGENTDOCK_TEST_CODEX_APP_SERVER")) == "" {
		t.Skip("AGENTDOCK_TEST_CODEX_APP_SERVER is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	client, err := Start(ctx, Config{CodexHome: os.Getenv("CODEX_HOME")})
	if err != nil {
		t.Fatalf("start codex app-server: %v", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()

	info := client.Info()
	t.Logf("initialize: userAgent=%q codexHome=%q platform=%s/%s", info.UserAgent, info.CodexHome, info.PlatformFamily, info.PlatformOS)
	if info.CodexHome == "" {
		t.Fatal("initialize did not report codexHome")
	}

	threadID, err := client.EnsureThread(ctx, os.TempDir())
	if err != nil {
		t.Fatalf("start thread: %v", err)
	}
	t.Logf("thread: %s", threadID)

	servers, err := client.ListMCPServers(ctx, threadID, "toolsAndAuthOnly")
	if err != nil {
		t.Fatalf("list MCP servers: %v", err)
	}
	if len(servers) == 0 {
		t.Fatal("codex reported no MCP servers")
	}
	var target *MCPServerStatus
	for index := range servers {
		server := servers[index]
		t.Logf("server: name=%q auth=%q runtime=%q tools=%d", server.Name, server.AuthStatus, server.RuntimeStatus, len(server.Tools))
		if target == nil && server.Name != "" {
			target = &servers[index]
		}
	}
	if target == nil {
		t.Fatal("no usable MCP server")
	}

	toolName := ""
	for key := range target.Tools {
		toolName = key
		break
	}
	if toolName == "" {
		t.Skipf("MCP server %q advertises no tools", target.Name)
	}
	result, err := client.CallMCPTool(ctx, target.Name, threadID, toolName, map[string]any{})
	if err != nil {
		t.Fatalf("call %s/%s: %v", target.Name, toolName, err)
	}
	t.Logf("%s/%s -> isError=%v text=%q images=%d", target.Name, toolName, result.IsError, truncate(result.Text(), 512), len(result.Images()))
	if len(result.Content) == 0 && len(result.StructuredContent) == 0 {
		t.Fatal("tool result carried no content")
	}
}
