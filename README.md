# omnibuf-nano

Lightweight C99 OmniBuf serialization library and `protoc` plugin for embedded systems.

## Design

- **Zero dynamic allocation:** No `malloc` or `free`. Generated C structs allocate fixed buffers from `max_size` and `max_count`.
- **Descriptor-table driven:** A shared C99 encoder and decoder interpret `const ob_msgdesc_t` tables placed in `RODATA`.
- **Buffer and callback streaming I/O:** Buffer-backed streams (`ob_ostream_from_buffer`) encode containers in a single pass via length backpatching. Custom `ob_ostream_t` and `ob_istream_t` callbacks stream directly to or from UART, SPI, or flash.
- **Native BSR options:** Protobuf bounds live in `omnibuf.nano.v1` (`buf.build/omnibuf/nano`) or sidecar `.options` files.

## Usage

```c
#include "ob_decode.h"
#include "ob_encode.h"
#include "sensor.ob.h"

uint8_t wire[128];
sensor_Reading out = { .device_id = 42, .celsius_x100 = -525 };

ob_ostream_t os = ob_ostream_from_buffer(wire, sizeof(wire));
if (!ob_encode(&os, &sensor_Reading_msg, &out)) {
    /* inspect os.errmsg */
}

sensor_Reading in;
ob_istream_t is = ob_istream_from_buffer(wire, os.bytes_written);
if (!ob_decode(&is, &sensor_Reading_msg, &in)) {
    /* inspect is.errmsg */
}
```

## Embedded Limitations

- **Field number limit:** Known fields in `ob_field_t` use `uint16_t` tags (`1..65535`). Wire tags above `65535` are skipped as unknown fields.
- **Strings:** C string fields are NUL-terminated fixed buffers (`char[max_size]`), so embedded `'\0'` bytes in `WireString` payloads are rejected on decode.
- **Unknown fields and `Field #0`:** Unknown wire fields and inline `Field #0` (`SchemaMeta`) blocks are skipped by framing length without deep schema or UTF-8 validation.

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
