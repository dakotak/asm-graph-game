//go:build js && wasm

// Command asmgraph-wasm is the asmgraph engine compiled to WebAssembly for
// the browser IDE. It runs the same protocol.Service and JSON-RPC
// dispatcher as `asmgraph serve --stdio` and `asmgraph web`, so the
// browser speaks exactly the same protocol, just without a server.
//
// It installs one global object:
//
//	asmgraphEngine.handle(message: string): string | null
//	    Handles one JSON-RPC message (or batch) and returns the response,
//	    or null for notifications.
//	asmgraphEngine.onEvent(fn: (notification: string) => void)
//	    Receives event notifications (JSON-RPC "event" messages), which
//	    may arrive at any time, including from asynchronous runs.
//
// Build with scripts/build-wasm.sh; the web IDE runs it in a Web Worker.
package main

import (
	"encoding/json"
	"log"
	"syscall/js"
	"time"

	"asmgraph/cells"
	"asmgraph/engine"
	"asmgraph/protocol"
	"asmgraph/transport/jsonrpc"
)

func main() {
	// Goroutines only let the browser in when they block, so unpaced runs
	// pause briefly every few milliseconds; "pause" and events stay live.
	engine.RunYield = 8 * time.Millisecond

	var listener js.Value
	notify := func(e engine.Event) {
		if listener.Type() != js.TypeFunction {
			return
		}
		b, err := json.Marshal(jsonrpc.EventNotification(e))
		if err != nil {
			log.Printf("asmgraph: encode event: %v", err)
			return
		}
		listener.Invoke(string(b))
	}
	svc := protocol.NewService(cells.NewRegistry(), notify)
	// There is no file system to load puzzles from: clients send YAML.
	svc.AllowFiles = false
	d := &jsonrpc.Dispatcher{Svc: svc}

	handle := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) < 1 || args[0].Type() != js.TypeString {
			return nil
		}
		resp := d.Handle([]byte(args[0].String()))
		if resp == nil {
			return nil
		}
		b, err := json.Marshal(resp)
		if err != nil {
			return `{"jsonrpc":"2.0","id":null,"error":{"code":-32603,"message":"cannot encode response"}}`
		}
		return string(b)
	})
	onEvent := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) > 0 {
			listener = args[0]
		}
		return nil
	})
	js.Global().Set("asmgraphEngine", js.ValueOf(map[string]any{
		"handle":          handle,
		"onEvent":         onEvent,
		"protocolVersion": protocol.Version,
	}))
	if ready := js.Global().Get("asmgraphEngineReady"); ready.Type() == js.TypeFunction {
		ready.Invoke()
	}
	select {} // keep the engine alive
}
