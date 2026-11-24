# omnibuf-nano

[![OmniBuf Spec: v1 DRAFT-1](https://img.shields.io/badge/OmniBuf%20Spec-v1%20DRAFT--1-FF4F18)](https://omnibuf.com/spec)

Zero-malloc C99 runtime and `protoc-gen-omnibuf-nano` code generator for embedded [OmniBuf Wire Specification v1 DRAFT-1](https://omnibuf.com/spec) targets. Full docs live at [omnibuf.com/nano](https://omnibuf.com/nano).

## Install

```bash
go install github.com/omnibuf/omnibuf-nano/cmd/protoc-gen-omnibuf-nano@latest
```

Compile `runtime/ob_varint.c`, `runtime/ob_encode.c`, and `runtime/ob_decode.c` with `-Iruntime -std=c99` alongside generated `.ob.h` and `.ob.c` files.

## Quick usage

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

## Implementation

- **Zero dynamic allocation:** No `malloc` or `free`. Generated C structs size fixed buffers from `max_size` and `max_count` options.
- **Descriptor tables in `RODATA`:** A shared C99 encoder and decoder interpret generated `const ob_msgdesc_t` tables.
- **Buffer and callback streams:** `ob_ostream_from_buffer` encodes containers in a single pass via length backpatching. Custom `ob_ostream_t` and `ob_istream_t` callbacks stream directly over UART, SPI, or flash.
- **Nanopb-style bounds:** Size limits live in `omnibuf.nano.v1` (`buf.build/omnibuf/nano`) Protobuf options or sidecar `.options` files with glob matching.
- **Embedded limits:** Known `ob_field_t` tags are `uint16_t` (`1..65535`); tags above `65535` are skipped as unknown. C strings are NUL-terminated fixed arrays (`char[max_size]`), so embedded `'\0'` bytes in wire strings fail decode. Unknown fields and `Field #0` blocks are skipped by framing length.
- **Verification:** Run C unit tests with ASan/UBSan (`tests/c_unit/`) and Go codegen/roundtrip tests via `go test -v ./...`.

## License

Apache-2.0
