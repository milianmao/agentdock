package codexhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Discovery describes how to reach a locally installed Codex CLI.
type Discovery struct {
	// Path is the resolved codex executable.
	Path string
	// CodexHome is the CODEX_HOME handed to the child process.
	CodexHome string
	// Source records which rule resolved Path: "config", "path", or "default".
	Source string
}

// DiscoverCodex resolves the codex executable and CODEX_HOME.
//
// The child environment is built from envstore.MinimalSystemEnv, which does not
// carry CODEX_HOME; without an explicit overlay Codex would silently fall back
// to $HOME/.codex and load a different plugin set than the user's shell.
func DiscoverCodex(configuredPath, configuredHome string) (Discovery, error) {
	discovery := Discovery{CodexHome: resolveCodexHome(configuredHome)}

	if path := strings.TrimSpace(configuredPath); path != "" {
		resolved, err := lookPath(path)
		if err != nil {
			return Discovery{}, newError(CodeCodexNotFound, "configured Codex executable was not found", false, map[string]any{"path": path}, err)
		}
		discovery.Path = resolved
		discovery.Source = "config"
		return discovery, nil
	}
	if resolved, err := lookPath("codex"); err == nil {
		discovery.Path = resolved
		discovery.Source = "path"
		return discovery, nil
	}
	return Discovery{}, newError(CodeCodexNotFound, "Codex CLI is not installed or not on PATH", false, map[string]any{
		"hint": "install the Codex CLI or set the Codex executable path",
	}, nil)
}

// resolveCodexHome mirrors Codex's own lookup: CODEX_HOME wins, then $HOME/.codex.
func resolveCodexHome(configured string) string {
	if home := strings.TrimSpace(configured); home != "" {
		return home
	}
	if home := strings.TrimSpace(os.Getenv("CODEX_HOME")); home != "" {
		return home
	}
	if dir, err := os.UserHomeDir(); err == nil && dir != "" {
		return filepath.Join(dir, ".codex")
	}
	return ""
}

// lookPath resolves an executable path, accepting explicit relative paths that
// contain a separator (exec.LookPath handles both, but we want the absolute
// form so error details are actionable).
func lookPath(name string) (string, error) {
	resolved, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(resolved) {
		return resolved, nil
	}
	absolute, err := filepath.Abs(resolved)
	if err != nil {
		return resolved, nil
	}
	return absolute, nil
}

// defaultThreadCWD is used when the caller supplies no working directory.
// Codex requires an existing directory for thread/start.
func defaultThreadCWD() string {
	if dir, err := os.UserHomeDir(); err == nil && dir != "" {
		return dir
	}
	return os.TempDir()
}
