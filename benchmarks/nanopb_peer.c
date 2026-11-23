#include "nanopb_peer.h"

#include <string.h>

#include "pb_decode.h"
#include "pb_encode.h"

/* Avoid linker symbol collision with omnibuf-nano's *_msg descriptors. */
#define omnibuf_bench_Location_msg npb_omnibuf_bench_Location_msg
#define omnibuf_bench_Neighbor_msg npb_omnibuf_bench_Neighbor_msg
#define omnibuf_bench_PeerUpdate_msg npb_omnibuf_bench_PeerUpdate_msg
#include "peer.pb.h"
#include "peer.pb.c"

static omnibuf_bench_PeerUpdate g_sample;

void nanopb_init_sample(void) {
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
    g_sample = msg;
}

size_t nanopb_encode_sample(uint8_t *buf, size_t bufsize) {
    pb_ostream_t stream = pb_ostream_from_buffer(buf, bufsize);
    if (!pb_encode(&stream, omnibuf_bench_PeerUpdate_fields, &g_sample)) {
        return 0u;
    }
    return stream.bytes_written;
}

bool nanopb_decode_sample(const uint8_t *buf, size_t len, uint64_t *seq_out) {
    omnibuf_bench_PeerUpdate dst = omnibuf_bench_PeerUpdate_init_zero;
    pb_istream_t stream = pb_istream_from_buffer(buf, len);
    if (!pb_decode(&stream, omnibuf_bench_PeerUpdate_fields, &dst)) {
        return false;
    }
    *seq_out = dst.sequence;
    return true;
}

size_t nanopb_struct_size(void) {
    return sizeof(omnibuf_bench_PeerUpdate);
}
