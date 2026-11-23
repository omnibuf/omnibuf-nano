#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>

#include "ob_common.h"
#include "ob_decode.h"
#include "ob_encode.h"
#include "conformance.ob.h"

int main(void) {
    static uint8_t in_buf[65536];
    static uint8_t out_buf[65536];

    size_t in_len = 0;
    for (;;) {
        size_t n = fread(in_buf + in_len, 1u, sizeof(in_buf) - in_len, stdin);
        in_len += n;
        if (n == 0u) {
            if (ferror(stdin)) {
                fprintf(stderr, "stdin read error\n");
                return 2;
            }
            break;
        }
        if (in_len == sizeof(in_buf)) {
            fprintf(stderr, "input exceeds 64 KiB buffer\n");
            return 2;
        }
    }

    omnibuf_nano_test_ConformanceMsg msg = omnibuf_nano_test_ConformanceMsg_init_zero;
    ob_istream_t is = ob_istream_from_buffer(in_buf, in_len);
    if (!ob_decode(&is, &omnibuf_nano_test_ConformanceMsg_msg, &msg)) {
        fprintf(stderr, "ob_decode failed: %s\n", is.errmsg ? is.errmsg : "unknown");
        return 1;
    }

    ob_ostream_t os = ob_ostream_from_buffer(out_buf, sizeof(out_buf));
    if (!ob_encode(&os, &omnibuf_nano_test_ConformanceMsg_msg, &msg)) {
        fprintf(stderr, "ob_encode failed: %s\n", os.errmsg ? os.errmsg : "unknown");
        return 1;
    }

    if (os.bytes_written > 0u) {
        if (fwrite(out_buf, 1u, os.bytes_written, stdout) != os.bytes_written) {
            fprintf(stderr, "stdout write error\n");
            return 2;
        }
    }
    return 0;
}
