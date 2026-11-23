#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
BUILD_DIR="$ROOT_DIR/benchmarks/build"
NANOPB_DIR="$BUILD_DIR/nanopb"
NANOPB_TAG="0.4.9.1"
GEN_DIR="$BUILD_DIR/gen"

mkdir -p "$GEN_DIR" "$ROOT_DIR/testdata"

# 1. Fetch pinned nanopb release (cached in benchmarks/build/nanopb).
if [[ ! -d "$NANOPB_DIR/.git" ]]; then
    git clone --depth 1 --branch "$NANOPB_TAG" https://github.com/nanopb/nanopb.git "$NANOPB_DIR"
fi

# 2. Build protoc-gen-omnibuf-nano.
PLUGIN_BIN="$BUILD_DIR/protoc-gen-omnibuf-nano"
(cd "$ROOT_DIR" && go build -o "$PLUGIN_BIN" ./cmd/protoc-gen-omnibuf-nano)

# 3. Generate omnibuf-nano C files.
protoc \
    -I"$ROOT_DIR/benchmarks" \
    --plugin="protoc-gen-omnibuf-nano=$PLUGIN_BIN" \
    --omnibuf-nano_out="$GEN_DIR" \
    --omnibuf-nano_opt="options_path=$ROOT_DIR/benchmarks" \
    "$ROOT_DIR/benchmarks/peer.proto"

# 4. Generate nanopb C files using nanopb's generator.
# Build nanopb's internal proto descriptor if needed.
if [[ ! -f "$NANOPB_DIR/generator/proto/nanopb_pb2.py" ]]; then
    (cd "$NANOPB_DIR/generator/proto" && protoc --python_out=. nanopb.proto)
fi
if command -v uv >/dev/null 2>&1; then
    uv run --with protobuf python3 "$NANOPB_DIR/generator/nanopb_generator.py" \
        -I "$ROOT_DIR/benchmarks" \
        -D "$GEN_DIR" \
        "$ROOT_DIR/benchmarks/peer.proto"
else
    if [[ ! -x "$BUILD_DIR/venv/bin/python3" ]]; then
        python3 -m venv "$BUILD_DIR/venv"
        "$BUILD_DIR/venv/bin/pip" install --quiet protobuf
    fi
    "$BUILD_DIR/venv/bin/python3" "$NANOPB_DIR/generator/nanopb_generator.py" \
        -I "$ROOT_DIR/benchmarks" \
        -D "$GEN_DIR" \
        "$ROOT_DIR/benchmarks/peer.proto"
fi

# 5. Compile and run PeerUpdate benchmark (-O3 -DNDEBUG).
CC="${CC:-cc}"
$CC -O3 -DNDEBUG -std=c99 -Wall -Wextra \
    -I"$ROOT_DIR/runtime" \
    -I"$ROOT_DIR/benchmarks" \
    -I"$GEN_DIR" \
    -I"$NANOPB_DIR" \
    "$ROOT_DIR/runtime/ob_varint.c" \
    "$ROOT_DIR/runtime/ob_encode.c" \
    "$ROOT_DIR/runtime/ob_decode.c" \
    "$GEN_DIR/peer.ob.c" \
    "$NANOPB_DIR/pb_common.c" \
    "$NANOPB_DIR/pb_encode.c" \
    "$NANOPB_DIR/pb_decode.c" \
    "$ROOT_DIR/benchmarks/nanopb_peer.c" \
    "$ROOT_DIR/benchmarks/peer_bench.c" \
    -o "$BUILD_DIR/peer_bench"

"$BUILD_DIR/peer_bench" "$ROOT_DIR/testdata/benchmarks.json"

# 6. Compare -Os runtime object sizes.
echo ""
echo "Runtime Object Size Comparison (-Os):"
mkdir -p "$BUILD_DIR/size_ob" "$BUILD_DIR/size_npb"
for f in ob_varint ob_encode ob_decode; do
    $CC -Os -std=c99 -I"$ROOT_DIR/runtime" -c "$ROOT_DIR/runtime/${f}.c" -o "$BUILD_DIR/size_ob/${f}.o"
done
for f in pb_common pb_encode pb_decode; do
    $CC -Os -std=c99 -I"$NANOPB_DIR" -c "$NANOPB_DIR/${f}.c" -o "$BUILD_DIR/size_npb/${f}.o"
done
echo "-- omnibuf-nano --"
size "$BUILD_DIR"/size_ob/*.o
echo "-- nanopb --"
size "$BUILD_DIR"/size_npb/*.o
