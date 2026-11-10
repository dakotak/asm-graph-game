package plugins_test

import (
	"errors"
	"testing"

	"asmgraph/cell"
	"asmgraph/cells"
	"asmgraph/plugins"
)

func TestStaticLoader(t *testing.T) {
	r := cell.NewRegistry()
	if err := plugins.LoadAll(r, plugins.Static{ID: "builtin", Register: cells.RegisterBuiltins}); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Lookup("stack"); !ok {
		t.Fatal("stack not registered")
	}
	// Loading twice fails on duplicate type registration.
	if err := plugins.LoadAll(r, plugins.Static{ID: "again", Register: cells.RegisterBuiltins}); err == nil {
		t.Fatal("duplicate registration accepted")
	}
	if err := (plugins.Static{ID: "nil"}).Load(r); err == nil {
		t.Fatal("nil register func accepted")
	}
}

func TestNativeLoaderMissingFile(t *testing.T) {
	err := plugins.LoadAll(cell.NewRegistry(), plugins.Native{Path: "/definitely/not/here.so"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !plugins.NativeSupported() && !errors.Is(err, plugins.ErrNativeUnsupported) {
		t.Fatalf("unsupported platform should report ErrNativeUnsupported, got %v", err)
	}
}
