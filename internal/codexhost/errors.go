package codexhost

import (
	"encoding/json"
	"errors"
)

// Error codes classify failures of the Codex app-server client. They describe
// the transport to the host process only; MCP-level tool failures arrive as
// ToolResult.IsError and must not be mapped onto these codes.
const (
	CodeCodexNotFound     = "CODEXHOST_CODEX_NOT_FOUND"
	CodeNotRunning        = "CODEXHOST_NOT_RUNNING"
	CodeStartFailed       = "CODEXHOST_START_FAILED"
	CodeProtocolError     = "CODEXHOST_PROTOCOL_ERROR"
	CodeConnectionClosed  = "CODEXHOST_CONNECTION_CLOSED"
	CodeConnectionFailed  = "CODEXHOST_CONNECTION_FAILED"
	CodeRequestCanceled   = "CODEXHOST_REQUEST_CANCELED"
	CodeInvalidResponse   = "CODEXHOST_INVALID_RESPONSE"
	CodeRemoteError       = "CODEXHOST_REMOTE_ERROR"
	CodeInvalidArgument   = "CODEXHOST_INVALID_ARGUMENT"
	CodeMessageTooLarge   = "CODEXHOST_MESSAGE_TOO_LARGE"
	CodeUnsupportedMethod = "CODEXHOST_UNSUPPORTED_METHOD"
)

// Error is the typed failure returned by every Client operation.
type Error struct {
	Code      string
	Message   string
	Retryable bool
	Details   map[string]any
	Cause     error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Cause != nil {
		return e.Message + ": " + e.Cause.Error()
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Cause }

func newError(code, message string, retryable bool, details map[string]any, cause error) *Error {
	return &Error{Code: code, Message: message, Retryable: retryable, Details: details, Cause: cause}
}

// ErrorCode returns the Codex host error code, or an empty string when err did
// not originate from this package.
func ErrorCode(err error) string {
	var hostErr *Error
	if errors.As(err, &hostErr) {
		return hostErr.Code
	}
	return ""
}

// RPCError is the JSON-RPC error object carried on the wire. It implements
// error so callers can inspect transport-level rejections (for example an
// unsupported method answered with -32601) via errors.As.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}
