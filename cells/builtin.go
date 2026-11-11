package cells

import "asmgraph/cell"

// Builtins returns fresh instances of every built-in architecture.
func Builtins() []cell.Architecture {
	return []cell.Architecture{NewStandard(), NewStackProcessor(), Stack{}, Input{}, Output{}}
}

// RegisterBuiltins registers every built-in architecture with r.
func RegisterBuiltins(r *cell.Registry) error {
	for _, a := range Builtins() {
		if err := r.Register(a); err != nil {
			return err
		}
	}
	return nil
}

// NewRegistry returns a registry pre-populated with the built-in types.
func NewRegistry() *cell.Registry {
	r := cell.NewRegistry()
	if err := RegisterBuiltins(r); err != nil {
		panic(err)
	}
	return r
}
