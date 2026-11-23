#define _POSIX_C_SOURCE 200809L

#include <assert.h>
#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

/* OmniBuf Nano headers */
#include "ob_common.h"
#include "ob_decode.h"
#include "ob_encode.h"
#include "peer.ob.h"

/* nanopb headers (prefixed via separate translation unit to avoid type name collision) */
#include "nanopb_peer.h"

static uint64_t now_ns(void) {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (uint64_t)ts.tv_sec * 1000000000ULL + (uint64_t)ts.tv_nsec;
}

static omnibuf_bench_PeerUpdate make_sample_ob(void) {
    omnibuf_bench_PeerUpdate msg = omnibuf_bench_PeerUpdate_init_zero;
    msg.asn = 64512u;
    strcpy(msg.router_id, "edge-rtr-fra01.core.backbone.net");
    msg.active = true;
    msg.uptime_sec = 1849203ULL;
    msg.rssi_dbm = -62;
    msg.cpu_load = 0.42875;

    msg.tags_count = 4u;
    strcpy(msg.tags[0], "tier1");
    strcpy(msg.tags[1], "ebgp");
    strcpy(msg.tags[2], "eu-central");
    strcpy(msg.tags[3], "ixp-de-cix");

    msg.prefixes_count = 6u;
    msg.prefixes[0] = 167772160u;
    msg.prefixes[1] = 172032000u;
    msg.prefixes[2] = 3232235520u;
    msg.prefixes[3] = 2886729728u;
    msg.prefixes[4] = 134744072u;
    msg.prefixes[5] = 134743040u;

    msg.has_location = true;
    strcpy(msg.location.region, "eu-central-1");
    strcpy(msg.location.datacenter, "FRA-Equinix-FR5");
    strcpy(msg.location.city, "Frankfurt");
    msg.location.rack = 42u;
    msg.location.latitude = 50.1109f;
    msg.location.longitude = 8.6821f;

    msg.neighbors_count = 4u;
    msg.neighbors[0].peer_asn = 3356u;
    strcpy(msg.neighbors[0].router, "ae0.fra1.level3.net");
    msg.neighbors[0].latency_us = 850u;
    msg.neighbors[0].established = true;

    msg.neighbors[1].peer_asn = 1299u;
    strcpy(msg.neighbors[1].router, "ffm-b5-link.telia.net");
    msg.neighbors[1].latency_us = 920u;
    msg.neighbors[1].established = true;

    msg.neighbors[2].peer_asn = 174u;
    strcpy(msg.neighbors[2].router, "be2040.ccr41.fra03.atlas.cogentco.com");
    msg.neighbors[2].latency_us = 1120u;
    msg.neighbors[2].established = true;

    msg.neighbors[3].peer_asn = 2914u;
    strcpy(msg.neighbors[3].router, "ae-12.r24.frnkge08.de.bb.gin.ntt.net");
    msg.neighbors[3].latency_us = 980u;
    msg.neighbors[3].established = false;

    msg.sequence = 9876543210ULL;
    return msg;
}

typedef double (*bench_fn_t)(size_t iters, void *ctx);

static double measure_ns_per_op(bench_fn_t fn, void *ctx) {
    /* Warmup */
    (void)fn(5000u, ctx);

    /* Calibrate to ~150 ms per run, take median of 5 runs */
    size_t iters = 50000u;
    double probe = fn(iters, ctx);
    if (probe > 0.0) {
        double target = 150000000.0 / probe;
        if (target < 10000.0) target = 10000.0;
        if (target > 5000000.0) target = 5000000.0;
        iters = (size_t)target;
    }

    double samples[5];
    for (int r = 0; r < 5; r++) {
        samples[r] = fn(iters, ctx);
    }
    /* Insertion sort 5 samples for median */
    for (int i = 1; i < 5; i++) {
        double key = samples[i];
        int j = i - 1;
        while (j >= 0 && samples[j] > key) {
            samples[j + 1] = samples[j];
            j--;
        }
        samples[j + 1] = key;
    }
    return samples[2];
}

typedef struct {
    omnibuf_bench_PeerUpdate msg;
    uint8_t wire[1024];
    size_t wire_len;
    volatile uint64_t sink;
} ob_ctx_t;

static double bench_ob_encode(size_t iters, void *arg) {
    ob_ctx_t *ctx = (ob_ctx_t *)arg;
    uint8_t buf[1024];
    uint64_t t0 = now_ns();
    size_t acc = 0;
    for (size_t i = 0; i < iters; i++) {
        ob_ostream_t os = ob_ostream_from_buffer(buf, sizeof(buf));
        if (!ob_encode(&os, &omnibuf_bench_PeerUpdate_msg, &ctx->msg)) {
            abort();
        }
        acc += os.bytes_written;
    }
    uint64_t dt = now_ns() - t0;
    ctx->sink = acc;
    return (double)dt / (double)iters;
}

static double bench_ob_decode(size_t iters, void *arg) {
    ob_ctx_t *ctx = (ob_ctx_t *)arg;
    omnibuf_bench_PeerUpdate dst;
    uint64_t t0 = now_ns();
    uint64_t acc = 0;
    for (size_t i = 0; i < iters; i++) {
        ob_istream_t is = ob_istream_from_buffer(ctx->wire, ctx->wire_len);
        if (!ob_decode(&is, &omnibuf_bench_PeerUpdate_msg, &dst)) {
            abort();
        }
        acc += dst.sequence;
    }
    uint64_t dt = now_ns() - t0;
    ctx->sink = acc;
    return (double)dt / (double)iters;
}

typedef struct {
    uint8_t wire[1024];
    size_t wire_len;
    volatile uint64_t sink;
} nanopb_ctx_t;

static double bench_nanopb_encode(size_t iters, void *arg) {
    nanopb_ctx_t *ctx = (nanopb_ctx_t *)arg;
    uint8_t buf[1024];
    uint64_t t0 = now_ns();
    size_t acc = 0;
    for (size_t i = 0; i < iters; i++) {
        size_t n = nanopb_encode_sample(buf, sizeof(buf));
        if (n == 0u) {
            abort();
        }
        acc += n;
    }
    uint64_t dt = now_ns() - t0;
    ctx->sink = acc;
    return (double)dt / (double)iters;
}

static double bench_nanopb_decode(size_t iters, void *arg) {
    nanopb_ctx_t *ctx = (nanopb_ctx_t *)arg;
    uint64_t t0 = now_ns();
    uint64_t acc = 0;
    for (size_t i = 0; i < iters; i++) {
        uint64_t seq = 0;
        if (!nanopb_decode_sample(ctx->wire, ctx->wire_len, &seq)) {
            abort();
        }
        acc += seq;
    }
    uint64_t dt = now_ns() - t0;
    ctx->sink = acc;
    return (double)dt / (double)iters;
}

int main(int argc, char **argv) {
    const char *json_out = (argc > 1) ? argv[1] : NULL;

    ob_ctx_t ob_ctx;
    memset(&ob_ctx, 0, sizeof(ob_ctx));
    ob_ctx.msg = make_sample_ob();
    {
        ob_ostream_t os = ob_ostream_from_buffer(ob_ctx.wire, sizeof(ob_ctx.wire));
        if (!ob_encode(&os, &omnibuf_bench_PeerUpdate_msg, &ob_ctx.msg)) {
            fprintf(stderr, "ob_encode failed: %s\n", os.errmsg ? os.errmsg : "unknown");
            return 1;
        }
        ob_ctx.wire_len = os.bytes_written;

        omnibuf_bench_PeerUpdate check;
        ob_istream_t is = ob_istream_from_buffer(ob_ctx.wire, ob_ctx.wire_len);
        if (!ob_decode(&is, &omnibuf_bench_PeerUpdate_msg, &check)) {
            fprintf(stderr, "ob_decode failed: %s\n", is.errmsg ? is.errmsg : "unknown");
            return 1;
        }
        if (check.asn != 64512u || check.sequence != 9876543210ULL ||
            strcmp(check.location.city, "Frankfurt") != 0 || check.neighbors_count != 4u) {
            fprintf(stderr, "ob_decode verification mismatch\n");
            return 1;
        }
    }

    nanopb_ctx_t npb_ctx;
    memset(&npb_ctx, 0, sizeof(npb_ctx));
    nanopb_init_sample();
    npb_ctx.wire_len = nanopb_encode_sample(npb_ctx.wire, sizeof(npb_ctx.wire));
    if (npb_ctx.wire_len == 0u) {
        fprintf(stderr, "nanopb encode failed\n");
        return 1;
    }
    {
        uint64_t seq = 0;
        if (!nanopb_decode_sample(npb_ctx.wire, npb_ctx.wire_len, &seq) || seq != 9876543210ULL) {
            fprintf(stderr, "nanopb decode verification failed\n");
            return 1;
        }
    }

    double ob_enc_ns = measure_ns_per_op(bench_ob_encode, &ob_ctx);
    double npb_enc_ns = measure_ns_per_op(bench_nanopb_encode, &npb_ctx);
    double ob_dec_ns = measure_ns_per_op(bench_ob_decode, &ob_ctx);
    double npb_dec_ns = measure_ns_per_op(bench_nanopb_decode, &npb_ctx);

    size_t ob_struct_bytes = sizeof(omnibuf_bench_PeerUpdate);
    size_t npb_struct_bytes = nanopb_struct_size();

    printf("PeerUpdate Benchmark (C99, -O3)\n");
    printf("==============================================================\n");
    printf("%-22s %10s %12s %10s %11s\n", "Library", "Encode ns", "Decode ns", "Wire B", "Struct B");
    printf("--------------------------------------------------------------\n");
    printf("%-22s %10.1f %12.1f %10zu %11zu\n",
           "omnibuf-nano", ob_enc_ns, ob_dec_ns, ob_ctx.wire_len, ob_struct_bytes);
    printf("%-22s %10.1f %12.1f %10zu %11zu\n",
           "nanopb", npb_enc_ns, npb_dec_ns, npb_ctx.wire_len, npb_struct_bytes);
    printf("--------------------------------------------------------------\n");
    printf("Speedup vs nanopb: Encode %.2fx, Decode %.2fx\n",
           npb_enc_ns / ob_enc_ns, npb_dec_ns / ob_dec_ns);

    if (json_out != NULL) {
        FILE *f = fopen(json_out, "w");
        if (f == NULL) {
            perror("fopen json_out");
            return 1;
        }
        fprintf(f,
            "{\n"
            "  \"language\": \"c99-nano\",\n"
            "  \"workload\": \"PeerUpdate\",\n"
            "  \"categories\": [\n"
            "    {\n"
            "      \"id\": \"encode\",\n"
            "      \"title\": \"Full Message Encode Speed\",\n"
            "      \"unit\": \"ns\",\n"
            "      \"results\": [\n"
            "        {\n"
            "          \"name\": \"OmniBuf Nano (C99)\",\n"
            "          \"nsPerOp\": %.1f,\n"
            "          \"opsPerSec\": %.0f,\n"
            "          \"wireBytes\": %zu,\n"
            "          \"structBytes\": %zu,\n"
            "          \"highlight\": true,\n"
            "          \"baseline\": true\n"
            "        },\n"
            "        {\n"
            "          \"name\": \"nanopb (C99)\",\n"
            "          \"nsPerOp\": %.1f,\n"
            "          \"opsPerSec\": %.0f,\n"
            "          \"wireBytes\": %zu,\n"
            "          \"structBytes\": %zu\n"
            "        }\n"
            "      ]\n"
            "    },\n"
            "    {\n"
            "      \"id\": \"decode\",\n"
            "      \"title\": \"Full Message Decode Speed\",\n"
            "      \"unit\": \"ns\",\n"
            "      \"results\": [\n"
            "        {\n"
            "          \"name\": \"OmniBuf Nano (C99)\",\n"
            "          \"nsPerOp\": %.1f,\n"
            "          \"opsPerSec\": %.0f,\n"
            "          \"highlight\": true,\n"
            "          \"baseline\": true\n"
            "        },\n"
            "        {\n"
            "          \"name\": \"nanopb (C99)\",\n"
            "          \"nsPerOp\": %.1f,\n"
            "          \"opsPerSec\": %.0f\n"
            "        }\n"
            "      ]\n"
            "    }\n"
            "  ]\n"
            "}\n",
            ob_enc_ns, 1e9 / ob_enc_ns, ob_ctx.wire_len, ob_struct_bytes,
            npb_enc_ns, 1e9 / npb_enc_ns, npb_ctx.wire_len, npb_struct_bytes,
            ob_dec_ns, 1e9 / ob_dec_ns,
            npb_dec_ns, 1e9 / npb_dec_ns);
        fclose(f);
    }

    return 0;
}
