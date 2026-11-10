package cell

import (
	"fmt"
	"regexp"
	"sort"
	"sync"
)

var typeNameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// Registry maps cell type identifiers to architectures. It is safe for
// concurrent use. Registration is expected to happen at startup; the
// engine only reads from it.
type Registry struct {
	mu    sync.RWMutex
	archs map[string]Architecture
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{archs: map[string]Architecture{}}
}

// Register adds an architecture. Type identifiers must be unique and
// match ^[a-z][a-z0-9_-]*$; a programmable architecture's ISA name must
// equal its type.
func (r *Registry) Register(a Architecture) error {
	if a == nil {
		return fmt.Errorf("cell registry: nil architecture")
	}
	t := a.Type()
	if !typeNameRe.MatchString(t) {
		return fmt.Errorf("cell registry: invalid type identifier %q", t)
	}
	if isa := a.ISA(); isa != nil && isa.Name() != t {
		return fmt.Errorf("cell registry: type %q has ISA named %q", t, isa.Name())
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.archs[t]; dup {
		return fmt.Errorf("cell registry: type %q is already registered", t)
	}
	r.archs[t] = a
	return nil
}

// Lookup returns the architecture registered under type t.
func (r *Registry) Lookup(t string) (Architecture, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.archs[t]
	return a, ok
}

// Types lists registered type identifiers in sorted order.
func (r *Registry) Types() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.archs))
	for t := range r.archs {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
