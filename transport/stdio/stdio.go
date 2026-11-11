// Package stdio serves the protocol as JSON-RPC 2.0 over newline-delimited
// JSON on standard input/output. stdout carries only protocol messages;
// logs go to the provided log writer (normally stderr).
package stdio

import (
	"bufio"
	"encoding/json"
	"io"
	"log"
	"sync"

	"asmgraph/cell"
	"asmgraph/engine"
	"asmgraph/protocol"
	"asmgraph/transport/jsonrpc"
)

// Server is a JSON-RPC 2.0 server bound to a reader and writer.
type Server struct {
	in  io.Reader
	out io.Writer
	log *log.Logger
	svc *protocol.Service
	rpc *jsonrpc.Dispatcher

	wmu sync.Mutex
}

// New creates a server. logw may be nil to discard logs.
func New(in io.Reader, out io.Writer, logw io.Writer, reg *cell.Registry) *Server {
	if logw == nil {
		logw = io.Discard
	}
	s := &Server{in: in, out: out, log: log.New(logw, "asmgraph: ", log.LstdFlags)}
	s.svc = protocol.NewService(reg, s.notify)
	s.rpc = &jsonrpc.Dispatcher{Svc: s.svc, Log: s.log}
	return s
}

// Service returns the underlying protocol service.
func (s *Server) Service() *protocol.Service { return s.svc }

// Serve processes requests until EOF or a shutdown request. Requests are
// handled in order; long runs use the asynchronous "run" command so that
// "pause" can be processed while a simulation is running.
func (s *Server) Serve() error {
	defer s.svc.Close()
	sc := bufio.NewScanner(s.in)
	sc.Buffer(make([]byte, 64*1024), 64<<20)
	s.log.Printf("serving JSON-RPC 2.0 on stdio (protocol %s)", protocol.Version)
	for sc.Scan() {
		if resp := s.rpc.Handle(sc.Bytes()); resp != nil {
			s.write(resp)
		}
		if s.svc.ShutdownRequested() {
			s.log.Printf("shutdown requested")
			return nil
		}
	}
	return sc.Err()
}

func (s *Server) notify(e engine.Event) {
	s.write(jsonrpc.EventNotification(e))
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
