// Package plugins loads cell architectures into a cell.Registry.
//
// Two loaders are provided:
//
//   - Static: a Go function compiled into the binary (the portable path;
//     built-in cells use it).
//   - Native: a Go plugin (.so built with -buildmode=plugin) opened at run
//     time. Native plugins only work on platforms supported by the Go
//     "plugin" package (linux, darwin, freebsd with cgo) and must be built
//     with the same Go toolchain and module versions as the host binary.
//
// A native plugin is a main package exporting:
//
//	var CellAPIVersion = cell.APIVersion         // optional but recommended
//	func RegisterCells(r *cell.Registry) error   // required
package plugins

import (
	"errors"
	"fmt"

	"asmgraph/cell"
)

// Symbols looked up in native plugins.
const (
	RegisterSymbol   = "RegisterCells"
	APIVersionSymbol = "CellAPIVersion"
)

// ErrNativeUnsupported is returned when native plugins are not supported
// by this build or platform.
var ErrNativeUnsupported = errors.New("native Go plugins are not supported on this platform/build")

// Loader registers zero or more architectures.
type Loader interface {
	Name() string
	Load(r *cell.Registry) error
}

// Static wraps a registration function compiled into the binary.
type Static struct {
	ID       string
	Register func(*cell.Registry) error
}

func (s Static) Name() string { return s.ID }
func (s Static) Load(r *cell.Registry) error {
	if s.Register == nil {
		return fmt.Errorf("static plugin %s: nil register function", s.ID)
	}
	return s.Register(r)
}

// Native loads a compiled Go plugin from Path.
type Native struct{ Path string }

func (n Native) Name() string                { return n.Path }
func (n Native) Load(r *cell.Registry) error { return loadNative(n.Path, r) }

// NativeSupported reports whether this binary can load native plugins.
func NativeSupported() bool { return nativeSupported }

// LoadAll runs each loader in order and stops at the first error.
func LoadAll(r *cell.Registry, loaders ...Loader) error {
	for _, l := range loaders {
		if err := l.Load(r); err != nil {
			return fmt.Errorf("plugin %s: %w", l.Name(), err)
		}
	}
	return nil
}
