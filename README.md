# omnibuf-nano

Lightweight C99 OmniBuf serialization library and `protoc` plugin for embedded systems.

## Design

- **Zero dynamic allocation:** No `malloc` or `free`. Generated C structs allocate fixed buffers from `max_size` and `max_count`.
- **Descriptor-table driven:** A shared C99 encoder and decoder interpret `const ob_msgdesc_t` tables placed in `RODATA`.
- **2-pass streaming I/O:** `ob_ostream_t` and `ob_istream_t` callbacks stream directly to or from UART, SPI, flash, or fixed buffers.
- **Native BSR options:** Protobuf bounds live in `omnibuf.nano.v1` (`buf.build/omnibuf/nano`) or sidecar `.options` files.

## Layout

- `proto/omnibuf/nano/v1/options.proto`: Native field, message, and file options.
- `runtime/`: Zero-dependency C99 library (`ob_common.h`, `ob_varint.c`, `ob_encode.c`, `ob_decode.c`).
- `cmd/protoc-gen-omnibuf-nano/`: Go `protoplugin` code generator emitting `.ob.h` and `.ob.c`.
- `internal/options/`: Sidecar `.options` parser and glob resolver.
- `internal/codegen/`: C header and descriptor table generator.
- `tests/c_unit/`: C99 runtime unit tests (ASan/UBSan).
- `tests/conformance/`: Cross-language Go OmniBuf to C99 Nano OmniBuf roundtrip suite.

## Quick Verification

```sh
# C99 runtime unit tests
cc -Wall -Wextra -Werror -std=c99 -pedantic -fsanitize=address,undefined \
  -Iruntime runtime/ob_varint.c runtime/ob_encode.c runtime/ob_decode.c \
  tests/c_unit/test_runtime.c -o tests/c_unit/build/test_runtime
./tests/c_unit/build/test_runtime

# Go options, codegen, and cross-language conformance tests
go test -v ./...
```
