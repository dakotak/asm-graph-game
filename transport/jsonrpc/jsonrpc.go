// Package jsonrpc implements JSON-RPC 2.0 message handling on top of a
// protocol.Service. Transports (stdio, WebSocket, HTTP) only move framed
// messages; this package parses requests, dispatches them, and builds
// responses, so every transport speaks exactly the same protocol.
package jsonrpc

import (
	"bytes"
	"encoding/json"
	"log"

	"asmgraph/engine"
	"asmgraph/protocol"
)

// Dispatcher turns raw JSON-RPC messages into responses.
type Dispatcher struct {
	Svc *protocol.Service
	Log *log.Logger
	// Allow, if set, restricts which methods may be called.
	Allow func(method string) bool
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// SuccessResponse is a JSON-RPC success response.
type SuccessResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result"`
}

// ErrorResponse is a JSON-RPC error response.
type ErrorResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Error   *protocol.Error `json:"error"`
}

// Notification is a server-to-client notification.
type Notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

// EventNotification wraps a simulation event as an "event" notification.
func EventNotification(e engine.Event) Notification {
	return Notification{"2.0", "event", e}
}

var nullID = json.RawMessage("null")

// Handle processes one message (a request, a notification, or a batch) and
// returns the response to send, or nil when no response is due.
func (d *Dispatcher) Handle(msg []byte) any {
	msg = bytes.TrimSpace(msg)
	if len(msg) == 0 {
		return nil
	}
	if msg[0] == '[' {
		var batch []json.RawMessage
		if err := json.Unmarshal(msg, &batch); err != nil {
			return parseError(err)
		}
		if len(batch) == 0 {
			return ErrorResponse{"2.0", nullID, &protocol.Error{Code: protocol.CodeInvalidRequest, Message: "empty batch"}}
		}
		var out []any
		for _, raw := range batch {
			if r := d.handleOne(raw); r != nil {
				out = append(out, r)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	return d.handleOne(msg)
}

func parseError(err error) any {
	return ErrorResponse{"2.0", nullID, &protocol.Error{Code: protocol.CodeParseError, Message: "parse error: " + err.Error()}}
}

func (d *Dispatcher) handleOne(raw json.RawMessage) any {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		if !json.Valid(raw) {
			return parseError(err)
		}
		return ErrorResponse{"2.0", nullID, &protocol.Error{Code: protocol.CodeInvalidRequest, Message: "request must be a JSON object"}}
	}
	id, hasID := fields["id"]
	if hasID && !validID(id) {
		return ErrorResponse{"2.0", nullID, &protocol.Error{Code: protocol.CodeInvalidRequest, Message: "id must be a string, number, or null"}}
	}
	respID := nullID
	if hasID {
		respID = id
	}
	var req request
	if err := json.Unmarshal(raw, &req); err != nil || req.JSONRPC != "2.0" || req.Method == "" {
		return ErrorResponse{"2.0", respID, &protocol.Error{Code: protocol.CodeInvalidRequest, Message: `invalid request: need "jsonrpc": "2.0" and a method`}}
	}
	var (
		result any
		perr   *protocol.Error
	)
	if d.Allow != nil && !d.Allow(req.Method) {
		perr = &protocol.Error{Code: protocol.CodeMethodNotFound, Message: "method " + req.Method + " is not available on this endpoint"}
	} else {
		result, perr = d.call(req.Method, req.Params)
	}
	if !hasID {
		if perr != nil {
			d.logf("notification %s failed: %v", req.Method, perr)
		}
		return nil
	}
	if perr != nil {
		return ErrorResponse{"2.0", respID, perr}
	}
	return SuccessResponse{"2.0", respID, result}
}

func (d *Dispatcher) call(method string, params json.RawMessage) (result any, perr *protocol.Error) {
	defer func() {
		if r := recover(); r != nil {
			d.logf("panic in %s: %v", method, r)
			result, perr = nil, &protocol.Error{Code: protocol.CodeInternalError, Message: "internal error"}
		}
	}()
	return d.Svc.Call(method, params)
}

func (d *Dispatcher) logf(format string, args ...any) {
	if d.Log != nil {
		d.Log.Printf(format, args...)
	}
}

func validID(id json.RawMessage) bool {
	t := bytes.TrimSpace(id)
	if len(t) == 0 {
		return false
	}
	switch t[0] {
	case '"':
		var s string
		return json.Unmarshal(t, &s) == nil
	case 'n':
		return string(t) == "null"
	default:
		var f float64
		return json.Unmarshal(t, &f) == nil
	}
}
