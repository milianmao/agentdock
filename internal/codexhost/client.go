package codexhost

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/uvwt/agentdock/internal/buildinfo"
	"github.com/uvwt/agentdock/internal/envstore"
	processcontrol "github.com/uvwt/agentdock/internal/process"
)

const (
	// initializeTimeout bounds the app-server handshake. Codex loads its plugin
	// catalogue before answering, so this is generous but still bounded.
	initializeTimeout = 30 * time.Second
	// threadTimeout bounds thread/start on the same grounds.
	threadTimeout = 30 * time.Second
	// stderrTailBytes keeps the tail of app-server stderr for error details.
	stderrTailBytes = 64 << 10
)

// Config configures one Codex app-server child process.
type Config struct {
	// ExecutablePath overrides Codex discovery. Empty means PATH lookup.
	ExecutablePath string
	// CodexHome overrides CODEX_HOME. Empty means $CODEX_HOME, then $HOME/.codex.
	CodexHome string
	// CWD is the working directory for the app-server process.
	CWD string
	// Environment overlays the minimal system environment handed to Codex.
	Environment map[string]string
	// ClientName/ClientVersion identify AgentDock in the initialize handshake.
	ClientName    string
	ClientVersion string
}

// Client owns one `codex app-server` child process speaking newline-delimited
// JSON-RPC over stdio.
type Client struct {
	config    Config
	discovery Discovery

	command     *exec.Cmd
	controller  *processcontrol.Controller
	stderr      *tailBuffer
	environment map[string]string

	transport *transport

	mu         sync.Mutex
	info       InitializeResult
	threadID   string
	clientInfo struct {
		Name    string
		Version string
	}

	closeOnce sync.Once
	closeErr  error
}

// Start launches `codex app-server` and completes the initialize handshake.
// On failure the child process is reaped before the error is returned.
func Start(ctx context.Context, cfg Config) (*Client, error) {
	discovery, err := DiscoverCodex(cfg.ExecutablePath, cfg.CodexHome)
	if err != nil {
		return nil, err
	}

	client := &Client{config: cfg, discovery: discovery}
	client.clientInfo.Name = strings.TrimSpace(cfg.ClientName)
	if client.clientInfo.Name == "" {
		client.clientInfo.Name = "agentdock"
	}
	client.clientInfo.Version = strings.TrimSpace(cfg.ClientVersion)
	if client.clientInfo.Version == "" {
		client.clientInfo.Version = buildinfo.Version
	}

	overlay := map[string]string{}
	for key, value := range cfg.Environment {
		overlay[key] = value
	}
	if discovery.CodexHome != "" {
		overlay["CODEX_HOME"] = discovery.CodexHome
	}
	client.environment = envstore.Merge(envstore.MinimalSystemEnv(), overlay)

	// The adapter lifetime exceeds the startup context, so the child is not tied
	// to ctx; shutdown is explicit through Close.
	command := exec.Command(discovery.Path, "app-server") //nolint:noctx // long-lived child
	command.Env = envstore.Format(client.environment)
	if cwd := strings.TrimSpace(cfg.CWD); cwd != "" {
		command.Dir = cwd
	}
	client.stderr = newTailBuffer(stderrTailBytes)
	command.Stderr = client.stderr

	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, newError(CodeStartFailed, "open Codex app-server stdin", false, nil, err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, newError(CodeStartFailed, "open Codex app-server stdout", false, nil, err)
	}

	configureChild(command)
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, newError(CodeStartFailed, "start Codex app-server", true, map[string]any{
			"path": discovery.Path, "codex_home": discovery.CodexHome,
		}, err)
	}
	client.command = command

	controller, err := processcontrol.Attach(command)
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = command.Process.Kill()
		_ = command.Wait()
		client.command = nil
		return nil, newError(CodeStartFailed, "attach Codex app-server process controller", false, nil, err)
	}
	client.controller = controller

	client.transport = newTransport(stdout, stdin, client.handleNotification)

	initializeCtx, cancel := context.WithTimeout(ctx, initializeTimeout)
	defer cancel()
	if _, err := client.Initialize(initializeCtx); err != nil {
		_ = client.Close()
		return nil, client.wrapError("initialize Codex app-server", err)
	}
	return client, nil
}

// EnsureThread returns the existing thread id, starting a new ephemeral thread
// when none exists. Computer-use tool calls require a thread id.
func (c *Client) EnsureThread(ctx context.Context, cwd string) (string, error) {
	if c == nil {
		return "", newError(CodeNotRunning, "codex host client is not running", false, nil, nil)
	}
	if threadID := c.ThreadID(); threadID != "" {
		return threadID, nil
	}
	threadCtx, cancel := context.WithTimeout(ctx, threadTimeout)
	defer cancel()
	if strings.TrimSpace(cwd) == "" {
		cwd = defaultThreadCWD()
	}
	return c.StartThread(threadCtx, ThreadStartParams{CWD: cwd, Ephemeral: true})
}

// Close terminates the child process tree. It is idempotent; a non-zero exit
// status from the killed child is not an error.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		if c.transport != nil {
			// EOF on the child's stdio is the normal terminal state; request
			// paths already surface unexpected failures with stderr context.
			_ = c.transport.Close()
		}
		if c.controller != nil {
			_ = c.controller.Terminate()
			c.closeErr = errors.Join(c.closeErr, c.controller.Close())
			c.controller = nil
		}
		if c.command != nil {
			if err := c.command.Wait(); err != nil {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) && !errors.Is(err, os.ErrProcessDone) {
					c.closeErr = errors.Join(c.closeErr, err)
				}
			}
			c.command = nil
		}
	})
	return c.closeErr
}

// Closed reports whether the child's stdio has reached a terminal state.
func (c *Client) Closed() <-chan struct{} {
	if c == nil || c.transport == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return c.transport.Closed()
}

// Stderr returns the retained tail of app-server stderr, redacted against the
// child environment.
func (c *Client) Stderr() string {
	if c == nil || c.stderr == nil {
		return ""
	}
	return redactEnvironmentValues(c.stderr.String(), c.environment)
}

// handleNotification records diagnostics and drops everything else. Codex emits
// configWarning and similar notifications on every start; a strict client that
// refused them would fail on ordinary machines.
func (c *Client) handleNotification(method string, params json.RawMessage) {
	switch method {
	case "configWarning", "warning":
		slog.Debug("codex app-server warning", "method", method, "detail", truncate(string(params), 2048))
	case "mcpServer/startupStatus/updated":
		slog.Debug("codex app-server MCP startup status", "detail", truncate(string(params), 2048))
	case "account/updated", "thread/started", "remoteControl/status/changed":
		slog.Debug("codex app-server notification", "method", method)
	default:
		slog.Debug("codex app-server notification dropped", "method", method)
	}
}

// wrapError attaches the retained stderr tail to a failure so operators can see
// why the child refused a request.
func (c *Client) wrapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var hostErr *Error
	if errors.As(err, &hostErr) {
		details := map[string]any{}
		if stderr := c.Stderr(); stderr != "" {
			details["stderr"] = truncate(stderr, stderrTailBytes)
		}
		for key, value := range hostErr.Details {
			details[key] = value
		}
		if len(details) == 0 {
			return hostErr
		}
		return newError(hostErr.Code, hostErr.Message, hostErr.Retryable, details, hostErr.Cause)
	}
	details := map[string]any{"operation": operation}
	if stderr := c.Stderr(); stderr != "" {
		details["stderr"] = truncate(stderr, stderrTailBytes)
	}
	return newError(CodeConnectionFailed, operation, true, details, err)
}

func redactEnvironmentValues(text string, environment map[string]string) string {
	if text == "" {
		return ""
	}
	redacted := text
	for _, value := range environment {
		if len(value) < 4 {
			continue
		}
		redacted = strings.ReplaceAll(redacted, value, "[REDACTED]")
	}
	return redacted
}

func truncate(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return text[:limit] + "...[truncated]"
}

// tailBuffer retains the last `limit` bytes written to it. Codex writes
// diagnostics to stderr continuously; only the tail is useful in an error.
type tailBuffer struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

func newTailBuffer(limit int) *tailBuffer {
	return &tailBuffer{limit: limit, data: make([]byte, 0, limit)}
}

func (b *tailBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	original := len(data)
	if b.limit <= 0 {
		return original, nil
	}
	if len(data) >= b.limit {
		b.data = append(b.data[:0], data[len(data)-b.limit:]...)
		return original, nil
	}
	if overflow := len(b.data) + len(data) - b.limit; overflow > 0 {
		copy(b.data, b.data[overflow:])
		b.data = b.data[:len(b.data)-overflow]
	}
	b.data = append(b.data, data...)
	return original, nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}

var _ io.Writer = (*tailBuffer)(nil)
