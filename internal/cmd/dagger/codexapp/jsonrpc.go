// Package codexapp implements the Codex app-server protocol -- the JSON-RPC
// interface OpenAI Codex exposes to IDEs and desktop apps -- on top of a Dagger
// agent session, so any client that speaks it can drive `dagger agent`.
//
// The protocol is JSON-RPC 2.0 in shape (requests, responses, notifications)
// framed as newline-delimited JSON, with one dialect quirk: Codex omits the
// "jsonrpc" version member. This package accepts it on input and never emits
// it, matching the reference implementation on the wire.
//
// The package is split in three: the framing (this file), the protocol types
// (protocol.go), and the server (server.go) that maps protocol threads and
// turns onto a Backend. The Backend is what the Dagger CLI provides; nothing in
// here talks to an engine directly, which is what keeps the protocol logic
// testable against a fake.
package codexapp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// JSON-RPC error codes. These are the standard ones; Codex uses the same
// numbers, so clients can branch on them.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// RPCError is the error member of a JSON-RPC error response. It doubles as a
// Go error so handlers can return one directly and have it relayed verbatim.
type RPCError struct {
	Code    int64  `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("%s (code %d)", e.Message, e.Code)
}

// Errorf builds an RPCError with a formatted message.
func Errorf(code int64, format string, args ...any) *RPCError {
	return &RPCError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Message is one JSON-RPC message as read off the wire. Which of request,
// notification or response it is follows from which members are set.
type Message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}

// IsRequest reports whether the message is a request expecting a response: it
// names a method and carries a non-null id.
func (m *Message) IsRequest() bool {
	return m.Method != "" && len(m.ID) > 0 && !bytes.Equal(m.ID, []byte("null"))
}

// IsNotification reports whether the message is a notification: a method with
// no id.
func (m *Message) IsNotification() bool {
	return m.Method != "" && !m.IsRequest()
}

// maxLineBytes bounds a single message. Image inputs travel inline as data
// URLs, so this is generous.
const maxLineBytes = 256 << 20

// Conn frames newline-delimited JSON-RPC messages over a reader/writer pair.
// Writes are serialized, so the server and the item stream can emit
// notifications from any goroutine.
type Conn struct {
	scanner *bufio.Scanner
	w       io.Writer
	wmu     sync.Mutex
}

// NewConn wraps r (client to server) and w (server to client).
func NewConn(r io.Reader, w io.Writer) *Conn {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxLineBytes)
	return &Conn{scanner: sc, w: w}
}

// Read returns the next message on the connection. It returns io.EOF once the
// peer closes its end, and an *RPCError with CodeParseError for a line that is
// not a JSON-RPC message -- the connection stays usable after one, so the
// caller can answer with the error and keep going.
func (c *Conn) Read() (*Message, error) {
	for c.scanner.Scan() {
		line := bytes.TrimSpace(c.scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var msg Message
		if err := json.Unmarshal(line, &msg); err != nil {
			return nil, &RPCError{Code: CodeParseError, Message: "parse error: " + err.Error()}
		}
		return &msg, nil
	}
	if err := c.scanner.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

type wireNotification struct {
	Method string `json:"method"`
	Params any    `json:"params"`
}

type wireResponse struct {
	ID     json.RawMessage `json:"id"`
	Result any             `json:"result"`
}

type wireError struct {
	ID    json.RawMessage `json:"id"`
	Error *RPCError       `json:"error"`
}

// Notify sends a notification.
func (c *Conn) Notify(method string, params any) error {
	if params == nil {
		params = struct{}{}
	}
	return c.write(wireNotification{Method: method, Params: params})
}

// Reply answers a request. A nil result is sent as an empty object, which is
// what the protocol's empty responses look like.
func (c *Conn) Reply(id json.RawMessage, result any) error {
	if result == nil {
		result = struct{}{}
	}
	return c.write(wireResponse{ID: id, Result: result})
}

// ReplyError answers a request with an error. A nil id (a parse error, where
// no request could be read) is sent as null.
func (c *Conn) ReplyError(id json.RawMessage, rpcErr *RPCError) error {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return c.write(wireError{ID: id, Error: rpcErr})
}

func (c *Conn) write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err = c.w.Write(data)
	return err
}
