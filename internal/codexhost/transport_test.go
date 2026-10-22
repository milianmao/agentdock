package codexhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeServer is a minimal app-server stand-in that records the bytes the client
// writes and lets a test push messages back.
type fakeServer struct {
	conn net.Conn

	mu       sync.Mutex
	received []rpcMessage
	notify   chan rpcMessage
}

func newFakeServer(t *testing.T) (*transport, *fakeServer) {
	t.Helper()
	clientSide, serverSide := net.Pipe()
	server := &fakeServer{conn: serverSide, notify: make(chan rpcMessage, 8)}
	go server.readLoop()
	return newTransport(clientSide, clientSide, func(string, json.RawMessage) {}), server
}

func (s *fakeServer) readLoop() {
	decoder := json.NewDecoder(s.conn)
	for {
		var message rpcMessage
		if err := decoder.Decode(&message); err != nil {
			close(s.notify)
			return
		}
		s.mu.Lock()
		s.received = append(s.received, message)
		s.mu.Unlock()
		if message.Method != "" && len(message.ID) > 0 {
			s.notify <- message
		}
	}
}

func (s *fakeServer) send(t *testing.T, message rpcMessage) {
	t.Helper()
	data, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, err := s.conn.Write(append(data, '\n')); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func (s *fakeServer) nextRequest(t *testing.T) rpcMessage {
	t.Helper()
	select {
	case message, ok := <-s.notify:
		if !ok {
			t.Fatal("server connection closed")
		}
		return message
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a client request")
	}
	return rpcMessage{}
}

func TestCallRoutesResponseByID(t *testing.T) {
	transport, server := newFakeServer(t)
	defer func() { _ = transport.Close() }()

	type reply struct {
		result json.RawMessage
	}
	done := make(chan reply, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		raw, err := transport.call(ctx, "initialize", map[string]any{"clientInfo": map[string]any{"name": "agentdock"}})
		if err != nil {
			done <- reply{}
			return
		}
		done <- reply{result: raw}
	}()

	message := server.nextRequest(t)
	if message.Method != "initialize" {
		t.Fatalf("unexpected method %q", message.Method)
	}
	server.send(t, rpcMessage{
		JSONRPC: "2.0",
		ID:      message.ID,
		Result:  json.RawMessage(`{"userAgent":"agentdock/1","codexHome":"/home/u/.codex","platformFamily":"unix","platformOs":"linux"}`),
	})

	select {
	case got := <-done:
		if len(got.result) == 0 {
			t.Fatal("expected a result")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("call did not complete")
	}
}

func TestServerRequestIsAnsweredWithDenial(t *testing.T) {
	transport, server := newFakeServer(t)
	defer func() { _ = transport.Close() }()

	id := json.RawMessage(`"srv-1"`)
	server.send(t, rpcMessage{JSONRPC: "2.0", ID: id, Method: methodFileChangeApproval, Params: json.RawMessage(`{}`)})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		server.mu.Lock()
		var response *rpcMessage
		for index := range server.received {
			message := server.received[index]
			if message.Method == "" && string(message.ID) == `"srv-1"` {
				response = &server.received[index]
				break
			}
		}
		server.mu.Unlock()
		if response == nil {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		if response.Error != nil {
			t.Fatalf("client answered with an error: %+v", response.Error)
		}
		var decision struct {
			Decision string `json:"decision"`
		}
		if err := json.Unmarshal(response.Result, &decision); err != nil {
			t.Fatalf("decode denial: %v", err)
		}
		if decision.Decision != "decline" {
			t.Fatalf("unexpected decision %q", decision.Decision)
		}
		return
	}
	t.Fatal("client did not answer the server request")
}

func TestUnknownNotificationIsDropped(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer func() { _ = serverSide.Close() }()

	notifications := make(chan string, 8)
	transport := newTransport(clientSide, clientSide, func(method string, _ json.RawMessage) {
		notifications <- method
	})
	defer func() { _ = transport.Close() }()

	payload := []byte(`{"jsonrpc":"2.0","method":"configWarning","params":{"summary":"unknown key"}}` + "\n")
	if _, err := serverSide.Write(payload); err != nil {
		t.Fatal(err)
	}
	select {
	case method := <-notifications:
		if method != "configWarning" {
			t.Fatalf("unexpected notification %q", method)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("notification was not delivered")
	}
	if err := transportCloseError(transport); err != nil {
		t.Fatalf("notification must not close the connection: %v", err)
	}
}

func TestCallAfterCloseFails(t *testing.T) {
	transport, _ := newFakeServer(t)
	if err := transport.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	_, err := transport.call(context.Background(), "initialize", nil)
	if err == nil {
		t.Fatal("expected an error after close")
	}
	if code := ErrorCode(err); code != CodeConnectionClosed {
		t.Fatalf("unexpected error code %q", code)
	}
}

func TestEOFRejectsPendingCalls(t *testing.T) {
	transport, server := newFakeServer(t)
	defer func() { _ = transport.Close() }()

	errCh := make(chan error, 1)
	go func() {
		_, err := transport.call(context.Background(), "thread/start", nil)
		errCh <- err
	}()
	_ = server.nextRequest(t)
	_ = server.conn.Close()

	select {
	case err := <-errCh:
		if !errors.Is(err, io.EOF) && ErrorCode(err) != CodeConnectionClosed {
			t.Fatalf("unexpected error %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pending call was not rejected on EOF")
	}
}

// transportCloseError reports the terminal error recorded by a closed transport.
func transportCloseError(transport *transport) error {
	select {
	case <-transport.Closed():
		return transport.closeErr
	default:
		return nil
	}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

func TestOversizeInboundMessageIsReportedAsTooLarge(t *testing.T) {
	reader, writer := io.Pipe()
	transport := newTransport(reader, nopWriteCloser{io.Discard}, nil)
	defer func() { _ = writer.Close() }()

	go func() {
		// One line above the documented limit; the scanner treats it as a
		// framing failure rather than a connectivity failure.
		_, _ = writer.Write(make([]byte, maxMessageBytes+1))
		_, _ = writer.Write([]byte("\n"))
	}()

	deadline := time.After(20 * time.Second)
	for {
		if err := transportCloseError(transport); err != nil {
			if code := ErrorCode(err); code != CodeMessageTooLarge {
				t.Fatalf("unexpected error code %q (err=%v)", code, err)
			}
			var hostErr *Error
			if !errors.As(err, &hostErr) {
				t.Fatalf("expected *Error, got %T", err)
			}
			if hostErr.Retryable {
				t.Fatal("an oversize inbound line is not retryable on the same connection")
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("transport did not terminate on an oversize inbound line")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// bufferWriteCloser records everything the transport writes so a test can prove
// an oversized message never reaches the child process.
type bufferWriteCloser struct{ bytes.Buffer }

func (*bufferWriteCloser) Close() error { return nil }

func TestWriteRejectsOversized(t *testing.T) {
	writer := &bufferWriteCloser{}
	transport := &transport{writer: writer, closed: make(chan struct{})}

	oversized := json.RawMessage(`"` + strings.Repeat("a", maxMessageBytes) + `"`)
	err := transport.writeMessage(rpcMessage{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "mcpServer/tool/call",
		Params:  oversized,
	})
	if code := ErrorCode(err); code != CodeMessageTooLarge {
		t.Fatalf("unexpected error code %q (err=%v)", code, err)
	}
	var hostErr *Error
	if !errors.As(err, &hostErr) {
		t.Fatalf("expected *Error, got %T", err)
	}
	if hostErr.Retryable {
		t.Fatal("an oversized outbound message cannot succeed on retry")
	}
	if limit, ok := hostErr.Details["limit"].(int); !ok || limit != maxMessageBytes {
		t.Fatalf("details must carry the limit, got %v", hostErr.Details)
	}
	if writer.Len() != 0 {
		t.Fatalf("oversized message was written (%d bytes)", writer.Len())
	}
}
