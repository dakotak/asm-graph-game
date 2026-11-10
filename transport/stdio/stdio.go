// Package stdio serves the protocol as JSON-RPC 2.0 over newline-delimited
// JSON on standard input/output. stdout carries only protocol messages;
// logs go to the provided log writer (normally stderr).
package stdio

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"log"
	"sync"

	"asmgraph/cell"
	"asmgraph/engine"
	"asmgraph/protocol"
)

// Server is a JSON-RPC 2.0 server bound to a reader and writer.
type Server struct {
	in  io.Reader
	out io.Writer
	log *log.Logger
	svc *protocol.Service

	wmu sync.Mutex
}

// New creates a server. logw may be nil to discard logs.
func New(in io.Reader, out io.Writer, logw io.Writer, reg *cell.Registry) *Server {
	if logw == nil {
		logw = io.Discard
	}
	s := &Server{in: in, out: out, log: log.New(logw, "asmgraph: ", log.LstdFlags)}
	s.svc = protocol.NewService(reg, s.notify)
	return s
}

// Service returns the underlying protocol service.
func (s *Server) Service() *protocol.Service { return s.svc }

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type successResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result"`
}

type errorResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Error   *protocol.Error `json:"error"`
}

type notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

var nullID = json.RawMessage("null")

// Serve processes requests until EOF or a shutdown request. Requests are
// handled in order; long runs use the asynchronous "run" command so that
// "pause" can be processed while a simulation is running.
func (s *Server) Serve() error {
	defer s.svc.Close()
	sc := bufio.NewScanner(s.in)
	sc.Buffer(make([]byte, 64*1024), 64<<20)
	s.log.Printf("serving JSON-RPC 2.0 on stdio (protocol %s)", protocol.Version)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if resp := s.handleMessage(line); resp != nil {
			s.write(resp)
		}
		if s.svc.ShutdownRequested() {
			s.log.Printf("shutdown requested")
			return nil
		}
	}
	return sc.Err()
}

func (s *Server) handleMessage(line []byte) any {
	if line[0] == '[' {
		var batch []json.RawMessage
		if err := json.Unmarshal(line, &batch); err != nil {
			return parseError(err)
		}
		if len(batch) == 0 {
			return errorResponse{"2.0", nullID, &protocol.Error{Code: protocol.CodeInvalidRequest, Message: "empty batch"}}
		}
		var out []any
		for _, raw := range batch {
			if r := s.handleOne(raw); r != nil {
				out = append(out, r)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	return s.handleOne(line)
}

func parseError(err error) any {
	return errorResponse{"2.0", nullID, &protocol.Error{Code: protocol.CodeParseError, Message: "parse error: " + err.Error()}}
}

func (s *Server) handleOne(raw json.RawMessage) any {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		if !json.Valid(raw) {
			return parseError(err)
		}
		return errorResponse{"2.0", nullID, &protocol.Error{Code: protocol.CodeInvalidRequest, Message: "request must be a JSON object"}}
	}
	id, hasID := fields["id"]
	if hasID && !validID(id) {
		return errorResponse{"2.0", nullID, &protocol.Error{Code: protocol.CodeInvalidRequest, Message: "id must be a string, number, or null"}}
	}
	respID := nullID
	if hasID {
		respID = id
	}
	var req request
	if err := json.Unmarshal(raw, &req); err != nil || req.JSONRPC != "2.0" || req.Method == "" {
		return errorResponse{"2.0", respID, &protocol.Error{Code: protocol.CodeInvalidRequest, Message: `invalid request: need "jsonrpc": "2.0" and a method`}}
	}
	result, perr := s.call(req.Method, req.Params)
	if !hasID {
		if perr != nil {
			s.log.Printf("notification %s failed: %v", req.Method, perr)
		}
		return nil
	}
	if perr != nil {
		return errorResponse{"2.0", respID, perr}
	}
	return successResponse{"2.0", respID, result}
}

func (s *Server) call(method string, params json.RawMessage) (result any, perr *protocol.Error) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Printf("panic in %s: %v", method, r)
			result, perr = nil, &protocol.Error{Code: protocol.CodeInternalError, Message: "internal error"}
		}
	}()
	return s.svc.Call(method, params)
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

func (s *Server) notify(e engine.Event) {
	s.write(notification{"2.0", "event", e})
}

func (s *Server) write(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		s.log.Printf("encode error: %v", err)
		return
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	b = append(b, '\n')
	if _, err := s.out.Write(b); err != nil {
		s.log.Printf("write error: %v", err)
	}
}
