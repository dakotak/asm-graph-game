//go:build (linux || darwin || freebsd) && cgo

package plugins

import (
	"fmt"
	"plugin"

	"asmgraph/cell"
)

const nativeSupported = true

func loadNative(path string, r *cell.Registry) error {
	p, err := plugin.Open(path)
	if err != nil {
		return err
	}
	if sym, err := p.Lookup(APIVersionSymbol); err == nil {
		v, ok := sym.(*int)
		if !ok {
			return fmt.Errorf("%s has type %T, want *int", APIVersionSymbol, sym)
		}
		if *v != cell.APIVersion {
			return fmt.Errorf("plugin targets cell API version %d, host provides %d", *v, cell.APIVersion)
		}
	}
	sym, err := p.Lookup(RegisterSymbol)
	if err != nil {
		return fmt.Errorf("missing %s: %w", RegisterSymbol, err)
	}
	fn, ok := sym.(func(*cell.Registry) error)
	if !ok {
		return fmt.Errorf("%s has type %T, want func(*cell.Registry) error", RegisterSymbol, sym)
	}
	return fn(r)
}
