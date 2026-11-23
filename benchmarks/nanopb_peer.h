#ifndef OMNIBUF_NANO_BENCH_NANOPB_PEER_H
#define OMNIBUF_NANO_BENCH_NANOPB_PEER_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

void nanopb_init_sample(void);
size_t nanopb_encode_sample(uint8_t *buf, size_t bufsize);
bool nanopb_decode_sample(const uint8_t *buf, size_t len, uint64_t *seq_out);
size_t nanopb_struct_size(void);

#endif /* OMNIBUF_NANO_BENCH_NANOPB_PEER_H */
