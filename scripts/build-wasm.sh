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
# Go 1.24+ keeps it in lib/wasm; older releases in misc/wasm.
for f in "$goroot/lib/wasm/wasm_exec.js" "$goroot/misc/wasm/wasm_exec.js"; do
  if [ -f "$f" ]; then
    cp "$f" "$out/wasm_exec.js"
    break
  fi
done
if [ ! -f "$out/wasm_exec.js" ] || ! cmp -s "$f" "$out/wasm_exec.js"; then
  echo "build-wasm: wasm_exec.js not found in $goroot (use Go 1.24 or later)" >&2
  exit 1
fi
echo "built $(du -h "$out/asmgraph.wasm" | cut -f1) $out/asmgraph.wasm"
