// Native Go plugin wrapper for the multiplier architecture.
//
//	go build -buildmode=plugin -o multiplier.so ./examples/plugins/multiplier/plugin
//	asmgraph test --plugin multiplier.so examples/plugins/multiplier/puzzle.yaml examples/plugins/multiplier/solution.yaml
package main

import (
	"asmgraph/cell"
	"asmgraph/examples/plugins/multiplier"
)

// CellAPIVersion lets the host reject plugins built for another contract.
var CellAPIVersion = cell.APIVersion

// RegisterCells is looked up by the host's native plugin loader.
func RegisterCells(r *cell.Registry) error { return multiplier.Register(r) }

// main is required for `go build ./...`; it never runs inside a plugin.
func main() {}
