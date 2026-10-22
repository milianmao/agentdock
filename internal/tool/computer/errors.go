package computer

import (
	"errors"
	"fmt"

	"github.com/uvwt/agentdock/internal/codexhost"
	toolcore "github.com/uvwt/agentdock/internal/tool/core"
)

// AgentDock 内部错误分类。官方 Computer Use 没有错误码 taxonomy：工具失败表现为
// content 文本 + isError:true，所以这里的 code 只用于 AgentDock 自己的日志与宿主面板，
// 不得对外声称与官方对齐。
const (
	ErrHostUnavailable  = "COMPUTER_HOST_UNAVAILABLE"
	ErrInvalidArguments = "COMPUTER_INVALID_ARGUMENTS"
	ErrCallFailed       = "COMPUTER_CALL_FAILED"
	ErrTimeout          = "COMPUTER_TIMEOUT"
)

func invalidArguments(message string) *toolcore.ToolError {
	return toolcore.NewError(ErrInvalidArguments, message, "validation")
}

// hostError converts a codexhost transport failure into a tool error. The Codex
// RPC code is preserved in details so support can distinguish "codex missing"
// from "connector rejected the call".
func hostError(operation string, err error) *toolcore.ToolError {
	if err == nil {
		return nil
	}
	details := map[string]any{"operation": operation}
	code := codexhost.ErrorCode(err)
	if code == "" {
		return toolcore.NewErrorCause(ErrCallFailed, operation, "runtime", details, err)
	}
	details["codex_code"] = code
	var hostErr *codexhost.Error
	if errors.As(err, &hostErr) {
		if len(hostErr.Details) > 0 {
			details["codex_details"] = hostErr.Details
		}
		if hostErr.Retryable {
			details["retryable"] = true
		}
	}
	switch code {
	case codexhost.CodeCodexNotFound, codexhost.CodeNotRunning, codexhost.CodeStartFailed:
		return toolcore.NewErrorCause(ErrHostUnavailable, fmt.Sprintf("Codex host is unavailable: %v", err), "runtime", details, err)
	case codexhost.CodeRequestCanceled:
		return toolcore.NewErrorCause(ErrTimeout, operation, "runtime", details, err)
	default:
		return toolcore.NewErrorCause(ErrCallFailed, fmt.Sprintf("%s: %v", operation, err), "runtime", details, err)
	}
}
