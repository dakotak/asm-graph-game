#!/bin/sh
# Builds the asmgraph engine as WebAssembly for the web IDE:
#   web/public/engine/asmgraph.wasm   the engine
#   web/public/engine/wasm_exec.js    Go's JS support code; it must come from
#                                     the same Go toolchain as the .wasm
set -eu
root=$(cd "$(dirname "$0")/.." && pwd)
out="$root/web/public/engine"
mkdir -p "$out"
cd "$root"
GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o "$out/asmgraph.wasm" ./cmd/asmgraph-wasm
goroot=$(go env GOROOT)
if [ -f "$goroot/lib/wasm/wasm_exec.js" ]; then
  cp "$goroot/lib/wasm/wasm_exec.js" "$out/wasm_exec.js"
else
  cp "$goroot/misc/wasm/wasm_exec.js" "$out/wasm_exec.js"
fi
echo "built $(du -h "$out/asmgraph.wasm" | cut -f1) $out/asmgraph.wasm"
