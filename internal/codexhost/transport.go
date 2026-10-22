package codexhost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"sync"
	"sync/atomic"
)

// maxMessageBytes bounds one JSON-RPC line. Computer-use tool results carry
// full accessibility trees plus inline screenshots, so the limit is far above
// the ACP one.
const maxMessageBytes = 32 << 20

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type rpcReply struct {
	result json.RawMessage
	err    error
}

// transport is a newline-delimited JSON-RPC 2.0 client over the app-server
// stdio pair. It routes responses by request id, answers every inbound server
// request through answerServerRequest, and drops notifications after logging.
type transport struct {
	reader io.ReadCloser
	writer io.WriteCloser

	writeMu sync.Mutex

	pendingMu sync.Mutex
	pending   map[string]chan rpcReply
	nextID    atomic.Uint64

	onNotification func(method string, params json.RawMessage)

	closed    chan struct{}
	closeOnce sync.Once
	closeErr  error
}

func newTransport(reader io.ReadCloser, writer io.WriteCloser, onNotification func(string, json.RawMessage)) *transport {
	connection := &transport{
		reader:         reader,
		writer:         writer,
		pending:        make(map[string]chan rpcReply),
		onNotification: onNotification,
		closed:         make(chan struct{}),
	}
	go connection.readLoop()
	return connection
}

// call performs one request and returns the raw result object.
func (c *transport) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if c == nil {
		return nil, newError(CodeNotRunning, "codex host client is not running", false, nil, nil)
	}
	id := c.nextID.Add(1)
	idRaw := json.RawMessage(strconv.FormatUint(id, 10))
	key := string(idRaw)
	reply := make(chan rpcReply, 1)
	paramsRaw, err := marshalRaw(params)
	if err != nil {
		return nil, newError(CodeProtocolError, "encode Codex request parameters", false, map[string]any{"method": method}, err)
	}

	c.pendingMu.Lock()
	select {
	case <-c.closed:
		c.pendingMu.Unlock()
		return nil, newError(CodeConnectionClosed, "Codex app-server connection is closed", true, nil, c.closeErr)
	default:
	}
	c.pending[key] = reply
	c.pendingMu.Unlock()

	if err := c.writeMessage(rpcMessage{JSONRPC: "2.0", ID: idRaw, Method: method, Params: paramsRaw}); err != nil {
		c.removePending(key)
		return nil, err
	}

	select {
	case answer := <-reply:
		if answer.err != nil {
			return nil, answer.err
		}
		return answer.result, nil
	case <-ctx.Done():
		c.removePending(key)
		return nil, newError(CodeRequestCanceled, "Codex request was canceled", true, map[string]any{"method": method}, ctx.Err())
	case <-c.closed:
		c.removePending(key)
		return nil, newError(CodeConnectionClosed, "Codex app-server connection closed before the response arrived", true, map[string]any{"method": method}, c.closeErr)
	}
}

// notify sends a JSON-RPC notification (no id, no response expected).
func (c *transport) notify(method string, params any) error {
	if c == nil {
		return newError(CodeNotRunning, "codex host client is not running", false, nil, nil)
	}
	paramsRaw, err := marshalRaw(params)
	if err != nil {
		return newError(CodeProtocolError, "encode Codex notification parameters", false, map[string]any{"method": method}, err)
	}
	return c.writeMessage(rpcMessage{JSONRPC: "2.0", Method: method, Params: paramsRaw})
}

func (c *transport) readLoop() {
	scanner := bufio.NewScanner(c.reader)
	scanner.Buffer(make([]byte, 64<<10), maxMessageBytes)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		if len(line) == 0 {
			continue
		}
		var message rpcMessage
		if err := json.Unmarshal(line, &message); err != nil {
			c.finish(newError(CodeProtocolError, "decode Codex JSON-RPC message", false, nil, err))
			return
		}
		switch {
		case message.Method != "" && len(message.ID) > 0 && string(message.ID) != "null":
			c.handleServerRequest(message)
		case message.Method != "":
			if c.onNotification != nil {
				c.onNotification(message.Method, message.Params)
			}
		case len(message.ID) == 0 || string(message.ID) == "null":
			c.finish(newError(CodeProtocolError, "Codex response omitted a request id", false, nil, nil))
			return
		default:
			c.handleResponse(message)
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		if errors.Is(err, bufio.ErrTooLong) {
			// An inbound line above the limit is a permanent framing failure:
			// the transport cannot resynchronise on a partial line, so this is
			// terminal, but the cause is size, not connectivity.
			c.finish(newError(CodeMessageTooLarge, "Codex JSON-RPC message exceeds limit", false, map[string]any{"limit": maxMessageBytes}, err))
			return
		}
		c.finish(newError(CodeConnectionFailed, "read Codex app-server stream", true, nil, err))
		return
	}
	c.finish(newError(CodeConnectionClosed, "Codex app-server stream reached EOF", true, nil, io.EOF))
}

// handleServerRequest answers app-server-initiated requests. A caller that
// never answers would stall the turn, so every branch — including unknown
// methods — writes exactly one response.
func (c *transport) handleServerRequest(message rpcMessage) {
	response := rpcMessage{JSONRPC: "2.0", ID: message.ID}
	var result any
	func() {
		defer func() {
			if recover() != nil {
				response.Result = nil
				response.Error = &RPCError{Code: -32603, Message: "Internal error"}
			}
		}()
		result, response.Error = answerServerRequest(message.Method, message.Params)
		if response.Error != nil {
			return
		}
		encoded, err := marshalRaw(result)
		if err != nil {
			response.Error = &RPCError{Code: -32603, Message: "Internal error"}
			return
		}
		response.Result = encoded
	}()
	_ = c.writeMessage(response)
}

func (c *transport) handleResponse(message rpcMessage) {
	key := string(message.ID)
	c.pendingMu.Lock()
	reply := c.pending[key]
	delete(c.pending, key)
	c.pendingMu.Unlock()
	if reply == nil {
		return
	}
	if message.Error != nil {
		details := map[string]any{"rpc_code": message.Error.Code}
		if len(message.Error.Data) > 0 {
			details["rpc_data"] = string(message.Error.Data)
		}
		reply <- rpcReply{err: newError(CodeRemoteError, message.Error.Message, false, details, message.Error)}
		return
	}
	reply <- rpcReply{result: message.Result}
}

func (c *transport) writeMessage(message rpcMessage) error {
	data, err := json.Marshal(message)
	if err != nil {
		return newError(CodeProtocolError, "encode Codex JSON-RPC message", false, nil, err)
	}
	if len(data) > maxMessageBytes {
		return newError(CodeMessageTooLarge, "Codex JSON-RPC message exceeds limit", false, map[string]any{"bytes": len(data), "limit": maxMessageBytes}, nil)
	}
	data = append(data, '\n')
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	select {
	case <-c.closed:
		return newError(CodeConnectionClosed, "Codex app-server connection is closed", true, nil, c.closeErr)
	default:
	}
	if _, err := c.writer.Write(data); err != nil {
		return newError(CodeConnectionFailed, "write Codex app-server stream", true, nil, err)
	}
	return nil
}

func (c *transport) removePending(key string) {
	c.pendingMu.Lock()
	delete(c.pending, key)
	c.pendingMu.Unlock()
}

func (c *transport) failPending(err error) {
	c.pendingMu.Lock()
	pending := c.pending
	c.pending = make(map[string]chan rpcReply)
	c.pendingMu.Unlock()
	for _, reply := range pending {
		reply <- rpcReply{err: err}
	}
}

func (c *transport) finish(err error) {
	c.closeOnce.Do(func() {
		c.closeErr = err
		if c.writer != nil {
			_ = c.writer.Close()
		}
		if c.reader != nil {
			// Closing a Windows anonymous-pipe reader while another goroutine is
			// blocked in Read can deadlock shutdown; publish the terminal state
			// synchronously and release the reader in the background.
			reader := c.reader
			go func() { _ = reader.Close() }()
		}
		close(c.closed)
		c.failPending(newError(CodeConnectionClosed, "Codex app-server connection closed", true, nil, c.closeErr))
	})
}

func (c *transport) Close() error {
	if c == nil {
		return nil
	}
	c.finish(nil)
	return c.closeErr
}

func (c *transport) Closed() <-chan struct{} { return c.closed }

func marshalRaw(value any) (json.RawMessage, error) {
	if value == nil {
		return json.RawMessage("null"), nil
	}
	if raw, ok := value.(json.RawMessage); ok {
		return raw, nil
	}
	return json.Marshal(value)
}
