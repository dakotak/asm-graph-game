//go:build !((linux || darwin || freebsd) && cgo)

package plugins

import "asmgraph/cell"

const nativeSupported = false

func loadNative(string, *cell.Registry) error { return ErrNativeUnsupported }
