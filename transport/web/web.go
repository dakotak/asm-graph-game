// Package web exposes the protocol to browsers.
//
//   - GET /api/ws        WebSocket carrying the same JSON-RPC 2.0 messages
//     as `asmgraph serve --stdio` (one message per text frame). Every
//     connection gets its own protocol.Service and simulation session.
//   - /api/puzzles…      HTTP endpoints for the puzzle library and for
//     reading and writing solution files (see docs/web.md).
//   - POST /api/rpc      Stateless JSON-RPC methods (initialize,
//     listCellTypes, validatePuzzle, assemble).
//   - everything else    The built frontend (StaticDir), with SPA fallback.
//
// The server never exposes arbitrary filesystem paths: puzzles come from
// PuzzlesDir and solutions live in SolutionsDir/<puzzle-id>/<name>.yaml,
// with both components validated.
package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"asmgraph/cell"
	"asmgraph/diag"
	"asmgraph/engine"
	"asmgraph/protocol"
	"asmgraph/puzzle"
	"asmgraph/solution"
	"asmgraph/transport/jsonrpc"
)

// Config configures the web server.
type Config struct {
	Registry     *cell.Registry
	PuzzlesDir   string
	SolutionsDir string
	// StaticDir holds the built frontend (web/dist). Empty disables it.
	StaticDir string
	Logger    *log.Logger
	// AllowedHosts are extra Host header values (without port) accepted in
	// addition to localhost, 127.0.0.1 and ::1. Requests with other hosts
	// are rejected to defend against DNS rebinding.
	AllowedHosts []string
	// OriginPatterns are extra WebSocket origin patterns (host[:port]
	// globs) accepted in addition to localhost origins.
	OriginPatterns []string
	// EventBuffer is the per-connection outbound queue size. Events that
	// do not fit are dropped; clients detect the seq gap and resync.
	EventBuffer int
}

// Server is the HTTP/WebSocket adapter.
type Server struct {
	cfg   Config
	log   *log.Logger
	mux   *http.ServeMux
	conns sync.WaitGroup
	wmu   sync.Mutex // serializes solution writes
}

// New creates a server.
func New(cfg Config) (*Server, error) {
	if cfg.Registry == nil {
		return nil, errors.New("web: registry is required")
	}
	if cfg.PuzzlesDir == "" {
		return nil, errors.New("web: puzzles directory is required")
	}
	if st, err := os.Stat(cfg.PuzzlesDir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("web: puzzles directory %q is not readable", cfg.PuzzlesDir)
	}
	if cfg.SolutionsDir == "" {
		return nil, errors.New("web: solutions directory is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = log.New(io.Discard, "", 0)
	}
	if cfg.EventBuffer <= 0 {
		cfg.EventBuffer = 512
	}
	s := &Server{cfg: cfg, log: cfg.Logger, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /api/health", s.health)
	s.mux.HandleFunc("GET /api/puzzles", s.listPuzzles)
	s.mux.HandleFunc("GET /api/puzzles/{id}", s.getPuzzle)
	s.mux.HandleFunc("GET /api/puzzles/{id}/solutions", s.listSolutions)
	s.mux.HandleFunc("GET /api/puzzles/{id}/solutions/{name}", s.getSolution)
	s.mux.HandleFunc("PUT /api/puzzles/{id}/solutions/{name}", s.putSolution)
	s.mux.HandleFunc("POST /api/rpc", s.rpc)
	s.mux.HandleFunc("GET /api/ws", s.ws)
	s.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "no such endpoint")
	})
	if cfg.StaticDir != "" {
		s.mux.Handle("/", spa(cfg.StaticDir))
	}
	return s, nil
}

// Handler returns the root handler with host checking and security headers.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hostAllowed(r.Host) {
			writeError(w, http.StatusForbidden, "host not allowed")
			return
		}
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
			"img-src 'self' data:; font-src 'self' data:; worker-src 'self' blob:; connect-src 'self' ws: wss:; frame-ancestors 'none'")
		s.mux.ServeHTTP(w, r)
	})
}

// Wait blocks until all WebSocket sessions have closed.
func (s *Server) Wait() { s.conns.Wait() }

func (s *Server) hostAllowed(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	switch host {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	for _, a := range s.cfg.AllowedHosts {
		if strings.EqualFold(a, host) {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// APIError is the body of every non-2xx HTTP API response.
type APIError struct {
	Error       string    `json:"error"`
	Diagnostics diag.List `json:"diagnostics,omitempty"`
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, APIError{Error: msg})
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "name": protocol.ServerName, "version": protocol.ServerVersion,
		"protocolVersion": protocol.Version,
	})
}

// --- puzzles -------------------------------------------------------------

// PuzzleSummary describes one puzzle file for the puzzle browser.
type PuzzleSummary struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Description       string    `json:"description"`
	File              string    `json:"file"`
	CellTypes         []string  `json:"cellTypes"`
	Cells             int       `json:"cells"`
	ProgrammableCells int       `json:"programmableCells"`
	Tests             int       `json:"tests"`
	Difficulty        any       `json:"difficulty,omitempty"`
	Valid             bool      `json:"valid"`
	Diagnostics       diag.List `json:"diagnostics,omitempty"`
	Solutions         int       `json:"solutions"`
}

type loadedPuzzle struct {
	summary PuzzleSummary
	puzzle  *puzzle.Puzzle
	yaml    []byte
}

func (s *Server) scanPuzzles() ([]loadedPuzzle, error) {
	entries, err := os.ReadDir(s.cfg.PuzzlesDir)
	if err != nil {
		return nil, err
	}
	var out []loadedPuzzle
	seen := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !(strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.cfg.PuzzlesDir, name))
		if err != nil || len(data) > 4<<20 {
			continue
		}
		p, l := puzzle.Parse(data, name)
		sum := PuzzleSummary{File: name, CellTypes: []string{}}
		if l.HasErrors() {
			sum.ID = strings.TrimSuffix(strings.TrimSuffix(name, ".yaml"), ".yml")
			sum.Name = sum.ID
			sum.Diagnostics = l
		} else {
			l = p.Validate(s.cfg.Registry)
			sum.ID, sum.Name, sum.Description = p.ID, p.Name, p.Description
			if sum.Name == "" {
				sum.Name = p.ID
			}
			sum.Valid = !l.HasErrors()
			sum.Diagnostics = l
			sum.Cells, sum.Tests = len(p.Cells), len(p.Tests)
			types := map[string]bool{}
			for _, c := range p.Cells {
				types[c.Type] = true
				if a, ok := s.cfg.Registry.Lookup(c.Type); ok && a.ISA() != nil {
					sum.ProgrammableCells++
				}
			}
			for t := range types {
				sum.CellTypes = append(sum.CellTypes, t)
			}
			sort.Strings(sum.CellTypes)
			if p.Metadata != nil {
				sum.Difficulty = p.Metadata["difficulty"]
			}
		}
		if seen[sum.ID] {
			continue // first file wins for duplicate ids
		}
		seen[sum.ID] = true
		sum.Solutions = len(s.solutionNames(sum.ID))
		out = append(out, loadedPuzzle{summary: sum, puzzle: p, yaml: data})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].summary.File < out[j].summary.File })
	return out, nil
}

func (s *Server) findPuzzle(id string) (*loadedPuzzle, error) {
	all, err := s.scanPuzzles()
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].summary.ID == id {
			return &all[i], nil
		}
	}
	return nil, nil
}

func (s *Server) listPuzzles(w http.ResponseWriter, _ *http.Request) {
	all, err := s.scanPuzzles()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot read puzzles directory")
		return
	}
	out := make([]PuzzleSummary, 0, len(all))
	for _, p := range all {
		out = append(out, p.summary)
	}
	writeJSON(w, http.StatusOK, map[string]any{"puzzles": out})
}

func (s *Server) getPuzzle(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !nameRe.MatchString(id) {
		writeError(w, http.StatusBadRequest, "invalid puzzle id")
		return
	}
	p, err := s.findPuzzle(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot read puzzles directory")
		return
	}
	if p == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no puzzle %q", id))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"summary": p.summary, "yaml": string(p.yaml), "puzzle": p.puzzle,
	})
}

// --- solutions -----------------------------------------------------------

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// SolutionInfo describes a saved solution file.
type SolutionInfo struct {
	Name     string    `json:"name"`
	Modified time.Time `json:"modified"`
	Size     int64     `json:"size"`
}

func (s *Server) solutionDir(puzzleID string) (string, error) {
	if !nameRe.MatchString(puzzleID) || strings.Contains(puzzleID, "..") {
		return "", errors.New("invalid puzzle id")
	}
	return filepath.Join(s.cfg.SolutionsDir, puzzleID), nil
}

func (s *Server) solutionPath(puzzleID, name string) (string, error) {
	dir, err := s.solutionDir(puzzleID)
	if err != nil {
		return "", err
	}
	if !nameRe.MatchString(name) || strings.Contains(name, "..") {
		return "", errors.New("invalid solution name (letters, digits, '.', '_', '-'; max 64)")
	}
	return filepath.Join(dir, name+".yaml"), nil
}

func (s *Server) solutionNames(puzzleID string) []SolutionInfo {
	dir, err := s.solutionDir(puzzleID)
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []SolutionInfo
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".yaml") || !e.Type().IsRegular() {
			continue
		}
		base := strings.TrimSuffix(n, ".yaml")
		if !nameRe.MatchString(base) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, SolutionInfo{Name: base, Modified: info.ModTime().UTC(), Size: info.Size()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *Server) listSolutions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.solutionDir(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	out := s.solutionNames(id)
	if out == nil {
		out = []SolutionInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"solutions": out})
}

func (s *Server) getSolution(w http.ResponseWriter, r *http.Request) {
	path, err := s.solutionPath(r.PathValue("id"), r.PathValue("name"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() {
		writeError(w, http.StatusNotFound, "no such solution")
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot read solution")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": r.PathValue("name"), "yaml": string(data)})
}

const maxSolutionBytes = 1 << 20

func (s *Server) putSolution(w http.ResponseWriter, r *http.Request) {
	id, name := r.PathValue("id"), r.PathValue("name")
	path, err := s.solutionPath(id, name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if p, err := s.findPuzzle(id); err != nil || p == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no puzzle %q", id))
		return
	}
	var body struct {
		YAML string `json:"yaml"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSolutionBytes+4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body must be {\"yaml\": string} under 1 MiB")
		return
	}
	// Work-in-progress solutions may not compile, but they must parse and
	// belong to this puzzle.
	sol, l := solution.Parse([]byte(body.YAML), name+".yaml")
	if l.HasErrors() {
		writeJSON(w, http.StatusUnprocessableEntity, APIError{Error: "solution YAML is invalid", Diagnostics: l})
		return
	}
	if sol.Puzzle != id {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("solution is for puzzle %q, not %q", sol.Puzzle, id))
		return
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "cannot create solutions directory")
		return
	}
	if st, err := os.Lstat(path); err == nil && !st.Mode().IsRegular() {
		writeError(w, http.StatusConflict, "refusing to overwrite a non-regular file")
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*.yaml")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot write solution")
		return
	}
	_, werr := tmp.WriteString(body.YAML)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Rename(tmp.Name(), path) != nil {
		os.Remove(tmp.Name())
		writeError(w, http.StatusInternalServerError, "cannot write solution")
		return
	}
	s.log.Printf("saved solution %s/%s", id, name)
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "saved": true})
}

// --- stateless RPC -------------------------------------------------------

// StatelessMethods may be called over POST /api/rpc.
var StatelessMethods = map[string]bool{
	"initialize": true, "listCellTypes": true, "validatePuzzle": true, "assemble": true,
}

func (s *Server) rpc(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "request too large")
		return
	}
	svc := protocol.NewService(s.cfg.Registry, nil)
	svc.AllowFiles = false
	defer svc.Close()
	d := &jsonrpc.Dispatcher{Svc: svc, Log: s.log, Allow: func(m string) bool { return StatelessMethods[m] }}
	resp := d.Handle(body)
	if resp == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- WebSocket session ---------------------------------------------------

func (s *Server) ws(w http.ResponseWriter, r *http.Request) {
	patterns := append([]string{"localhost", "localhost:*", "127.0.0.1", "127.0.0.1:*", "[::1]", "[::1]:*"}, s.cfg.OriginPatterns...)
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: patterns})
	if err != nil {
		s.log.Printf("websocket accept: %v", err)
		return
	}
	c.SetReadLimit(4 << 20)
	s.conns.Add(1)
	defer s.conns.Done()
	s.serveConn(r.Context(), c)
}

type wsConn struct {
	out     chan []byte
	dropped atomic.Int64
}

func (s *Server) serveConn(parent context.Context, c *websocket.Conn) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	conn := &wsConn{out: make(chan []byte, s.cfg.EventBuffer)}

	svc := protocol.NewService(s.cfg.Registry, func(e engine.Event) {
		b, err := json.Marshal(jsonrpc.EventNotification(e))
		if err != nil {
			return
		}
		// Never block the simulation on a slow client: drop the event. The
		// service numbers events without gaps, so the client sees the gap
		// and resynchronizes with getState.
		select {
		case conn.out <- b:
		default:
			conn.dropped.Add(1)
		}
	})
	svc.AllowFiles = false
	defer svc.Close()
	d := &jsonrpc.Dispatcher{Svc: svc, Log: s.log}

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for {
			select {
			case <-ctx.Done():
				return
			case b := <-conn.out:
				wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
				err := c.Write(wctx, websocket.MessageText, b)
				wcancel()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()

	s.log.Printf("websocket session opened")
	for {
		typ, msg, err := c.Read(ctx)
		if err != nil {
			break
		}
		if typ != websocket.MessageText {
			continue
		}
		resp := d.Handle(msg)
		if resp != nil {
			b, err := json.Marshal(resp)
			if err != nil {
				continue
			}
			select { // responses are never dropped
			case conn.out <- b:
			case <-ctx.Done():
			}
		}
		if svc.ShutdownRequested() {
			break
		}
	}
	svc.Close()
	cancel()
	<-writerDone
	c.Close(websocket.StatusNormalClosure, "")
	if n := conn.dropped.Load(); n > 0 {
		s.log.Printf("websocket session closed (%d events dropped for backpressure)", n)
	} else {
		s.log.Printf("websocket session closed")
	}
}

// --- static frontend -----------------------------------------------------

// spa serves files from dir and falls back to index.html for client-side
// routes. Only regular files inside dir are served.
func spa(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := filepath.Clean("/" + r.URL.Path)
		p := filepath.Join(dir, filepath.FromSlash(clean))
		if st, err := os.Stat(p); err != nil || st.IsDir() && clean != "/" {
			http.ServeFile(w, r, filepath.Join(dir, "index.html"))
			return
		}
		if strings.HasPrefix(clean, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		fs.ServeHTTP(w, r)
	})
}
