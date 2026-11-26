#include <assert.h>
#include <float.h>
#include <limits.h>
#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "ob_common.h"
#include "ob_decode.h"
#include "ob_encode.h"
#include "ob_varint.h"

/* ---------- Varint & ZigZag & UTF-8 Tests ---------- */

static void test_varint_boundaries(void) {
    const uint64_t values[] = {
        0u, 1u, 63u, 127u,
        128u, 129u, 16383u,
        16384u, 65535u, 536870911u,
        536870912u, 0xFFFFFFFFULL, 0x7FFFFFFFFFFFFFFFULL, UINT64_MAX
    };
    const size_t expected_sizes[] = {
        1u, 1u, 1u, 1u,
        2u, 2u, 2u,
        4u, 4u, 4u,
        9u, 9u, 9u, 9u
    };
    size_t count = sizeof(values) / sizeof(values[0]);

    for (size_t i = 0; i < count; i++) {
        uint8_t buf[16];
        ob_ostream_t os = ob_ostream_from_buffer(buf, sizeof(buf));
        assert(ob_varint_size(values[i]) == expected_sizes[i]);
        assert(ob_varint_encode(&os, values[i]));
        assert(os.bytes_written == expected_sizes[i]);

        ob_istream_t is = ob_istream_from_buffer(buf, os.bytes_written);
        uint64_t decoded = 0;
        assert(ob_varint_decode(&is, &decoded));
        assert(decoded == values[i]);
        assert(is.bytes_left == 0u);
    }

    /* Overlong 2-byte encoding of 5: (5 << 2) | 2 = 0x16, 0x00 */
    {
        const uint8_t overlong[2] = {0x16u, 0x00u};
        ob_istream_t is = ob_istream_from_buffer(overlong, sizeof(overlong));
        uint64_t decoded = 0;
        assert(ob_varint_decode(&is, &decoded));
        assert(decoded == 5u);
        assert(is.bytes_left == 0u);
    }

    /* Reserved prefix bytes (low 3 bits == 0, non-zero byte) must fail */
    {
        const uint8_t reserved[] = {0x08u, 0x10u, 0x80u, 0xF8u};
        for (size_t i = 0; i < sizeof(reserved); i++) {
            uint8_t buf[9] = {reserved[i], 0, 0, 0, 0, 0, 0, 0, 0};
            ob_istream_t is = ob_istream_from_buffer(buf, sizeof(buf));
            uint64_t decoded = 0;
            assert(!ob_varint_decode(&is, &decoded));
            assert(is.errmsg != NULL);
        }
    }

    /* Truncated 2-byte, 4-byte, and 9-byte varints must fail */
    {
        const uint8_t trunc2[1] = {0x02u};
        const uint8_t trunc4[3] = {0x04u, 0x00u, 0x00u};
        const uint8_t trunc9[8] = {0x00u, 1, 2, 3, 4, 5, 6, 7};
        uint64_t decoded = 0;

        ob_istream_t is2 = ob_istream_from_buffer(trunc2, sizeof(trunc2));
        assert(!ob_varint_decode(&is2, &decoded));

        ob_istream_t is4 = ob_istream_from_buffer(trunc4, sizeof(trunc4));
        assert(!ob_varint_decode(&is4, &decoded));

        ob_istream_t is9 = ob_istream_from_buffer(trunc9, sizeof(trunc9));
        assert(!ob_varint_decode(&is9, &decoded));
    }
}

static void test_zigzag(void) {
    const int64_t values[] = {
        0, -1, 1, -2, 2, -128, 127, -32768, 32767,
        INT32_MIN, INT32_MAX, INT64_MIN, INT64_MAX
    };
    size_t count = sizeof(values) / sizeof(values[0]);
    for (size_t i = 0; i < count; i++) {
        uint64_t enc = ob_zigzag_encode(values[i]);
        int64_t dec = ob_zigzag_decode(enc);
        assert(dec == values[i]);
    }
    assert(ob_zigzag_encode(0) == 0u);
    assert(ob_zigzag_encode(-1) == 1u);
    assert(ob_zigzag_encode(1) == 2u);
    assert(ob_zigzag_encode(-2) == 3u);
    assert(ob_zigzag_encode(INT64_MIN) == UINT64_MAX);
}

static void test_utf8(void) {
    const uint8_t ok_ascii[] = "hello world";
    const uint8_t ok_2b[] = {0xC3u, 0xA9u};                     /* e-acute */
    const uint8_t ok_3b[] = {0xE2u, 0x82u, 0xACu};              /* Euro */
    const uint8_t ok_4b[] = {0xF0u, 0x9Fu, 0x98u, 0x80u};       /* Grinning face */
    const uint8_t bad_overlong[] = {0xC0u, 0xAFu};
    const uint8_t bad_surrogate[] = {0xEDu, 0xA0u, 0x80u};      /* U+D800 */
    const uint8_t bad_high[] = {0xF4u, 0x90u, 0x80u, 0x80u};    /* U+110000 */
    const uint8_t bad_trunc[] = {0xE2u, 0x82u};

    assert(ob_valid_utf8(ok_ascii, sizeof(ok_ascii) - 1u));
    assert(ob_valid_utf8(ok_2b, sizeof(ok_2b)));
    assert(ob_valid_utf8(ok_3b, sizeof(ok_3b)));
    assert(ob_valid_utf8(ok_4b, sizeof(ok_4b)));
    assert(!ob_valid_utf8(bad_overlong, sizeof(bad_overlong)));
    assert(!ob_valid_utf8(bad_surrogate, sizeof(bad_surrogate)));
    assert(!ob_valid_utf8(bad_high, sizeof(bad_high)));
    assert(!ob_valid_utf8(bad_trunc, sizeof(bad_trunc)));
}

/* ---------- Manual Descriptor Struct Tests ---------- */

typedef struct {
    int32_t id;
    char label[32];
} SubMsg;

static const ob_field_t SubMsg_fields[] = {
    {1u, OB_WIRE_SINT,   OB_FLAG_SINGULAR, offsetof(SubMsg, id),    0u, sizeof(int32_t), 1u, NULL},
    {2u, OB_WIRE_STRING, OB_FLAG_SINGULAR, offsetof(SubMsg, label), 0u, 32u,             1u, NULL}
};
static const ob_msgdesc_t SubMsg_msg = {SubMsg_fields, 2u, sizeof(SubMsg)};

typedef struct {
    char key[16];
    uint64_t value;
} MapEntry;

static const ob_field_t MapEntry_fields[] = {
    {1u, OB_WIRE_STRING, OB_FLAG_SINGULAR, offsetof(MapEntry, key),   0u, 16u,              1u, NULL},
    {2u, OB_WIRE_UINT,   OB_FLAG_SINGULAR, offsetof(MapEntry, value), 0u, sizeof(uint64_t), 1u, NULL}
};
static const ob_msgdesc_t MapEntry_msg = {MapEntry_fields, 2u, sizeof(MapEntry)};

typedef struct {
    uint32_t u32;
    int64_t s64;
    bool flag;
    float f32;
    double f64;
    uint32_t fix32;
    int64_t sfix64;
    char name[32];
    OB_BYTES_ARRAY_T(16) raw;
    uint8_t uuid[4];
    bool has_sub;
    SubMsg sub;
    uint16_t nums_count;
    uint32_t nums[8];
    uint16_t subs_count;
    SubMsg subs[4];
    uint16_t kv_count;
    MapEntry kv[4];
    uint16_t which_choice;
    union {
        int32_t choice_int;
        char choice_str[16];
    } choice;
    bool has_opt_bool;
    bool opt_bool;
    uint32_t fixed_arr[3];
} AllMsg;

static const ob_field_t AllMsg_fields[] = {
    {1u,  OB_WIRE_UINT,       OB_FLAG_SINGULAR, offsetof(AllMsg, u32),    0u, sizeof(uint32_t), 1u, NULL},
    {2u,  OB_WIRE_SINT,       OB_FLAG_SINGULAR, offsetof(AllMsg, s64),    0u, sizeof(int64_t),  1u, NULL},
    {3u,  OB_WIRE_BOOL_FALSE, OB_FLAG_SINGULAR, offsetof(AllMsg, flag),   0u, sizeof(bool),     1u, NULL},
    {4u,  OB_WIRE_FLOAT32,    OB_FLAG_SINGULAR, offsetof(AllMsg, f32),    0u, sizeof(float),    1u, NULL},
    {5u,  OB_WIRE_FLOAT64,    OB_FLAG_SINGULAR, offsetof(AllMsg, f64),    0u, sizeof(double),   1u, NULL},
    {6u,  OB_WIRE_FIXED32,    OB_FLAG_SINGULAR, offsetof(AllMsg, fix32),  0u, sizeof(uint32_t), 1u, NULL},
    {7u,  OB_WIRE_SFIXED64,   OB_FLAG_SINGULAR, offsetof(AllMsg, sfix64), 0u, sizeof(int64_t),  1u, NULL},
    {8u,  OB_WIRE_STRING,     OB_FLAG_SINGULAR, offsetof(AllMsg, name),   0u, 32u,              1u, NULL},
    {9u,  OB_WIRE_BYTES,      OB_FLAG_SINGULAR, offsetof(AllMsg, raw),    0u, 16u,              1u, NULL},
    {10u, OB_WIRE_BYTES,      OB_FLAG_FIXED_LEN, offsetof(AllMsg, uuid),  0u, 4u,               1u, NULL},
    {11u, OB_WIRE_SUBMSG,     OB_FLAG_OPTIONAL, offsetof(AllMsg, sub),    offsetof(AllMsg, has_sub),    sizeof(SubMsg),   1u, &SubMsg_msg},
    {12u, OB_WIRE_UINT,       OB_FLAG_REPEATED, offsetof(AllMsg, nums),   offsetof(AllMsg, nums_count), sizeof(uint32_t), 8u, NULL},
    {13u, OB_WIRE_SUBMSG,     OB_FLAG_REPEATED, offsetof(AllMsg, subs),   offsetof(AllMsg, subs_count), sizeof(SubMsg),   4u, &SubMsg_msg},
    {14u, OB_WIRE_MAP,        OB_FLAG_MAP,      offsetof(AllMsg, kv),     offsetof(AllMsg, kv_count),   sizeof(MapEntry), 4u, &MapEntry_msg},
    {15u, OB_WIRE_SINT,       OB_FLAG_ONEOF,    offsetof(AllMsg, choice.choice_int), offsetof(AllMsg, which_choice), sizeof(int32_t), 1u, NULL},
    {16u, OB_WIRE_STRING,     OB_FLAG_ONEOF,    offsetof(AllMsg, choice.choice_str), offsetof(AllMsg, which_choice), 16u,             1u, NULL},
    {17u, OB_WIRE_BOOL_FALSE, OB_FLAG_OPTIONAL, offsetof(AllMsg, opt_bool), offsetof(AllMsg, has_opt_bool), sizeof(bool), 1u, NULL},
    {18u, OB_WIRE_UINT,       OB_FLAG_REPEATED | OB_FLAG_FIXED_LEN, offsetof(AllMsg, fixed_arr), 0u, sizeof(uint32_t), 3u, NULL}
};
static const ob_msgdesc_t AllMsg_msg = {AllMsg_fields, 18u, sizeof(AllMsg)};

/* Byte-by-byte streaming callback to verify true streaming I/O. */
typedef struct {
    uint8_t *buf;
    size_t pos;
    size_t cap;
    size_t call_count;
} chunk_stream_state_t;

static bool byte_write_cb(ob_ostream_t *stream, const uint8_t *buf, size_t count) {
    chunk_stream_state_t *st = (chunk_stream_state_t *)stream->state;
    if (st->pos + count > st->cap) {
        return false;
    }
    memcpy(st->buf + st->pos, buf, count);
    st->pos += count;
    st->call_count++;
    return true;
}

static bool byte_read_cb(ob_istream_t *stream, uint8_t *buf, size_t count) {
    chunk_stream_state_t *st = (chunk_stream_state_t *)stream->state;
    if (st->pos + count > st->cap) {
        return false;
    }
    memcpy(buf, st->buf + st->pos, count);
    st->pos += count;
    st->call_count++;
    return true;
}

static void test_full_roundtrip(void) {
    AllMsg src;
    memset(&src, 0, sizeof(src));
    src.u32 = 42000u;
    src.s64 = -9876543210LL;
    src.flag = true;
    src.f32 = -12.5f;
    src.f64 = 3.141592653589793;
    src.fix32 = 0xDEADBEEFu;
    src.sfix64 = -123456789012345LL;
    strcpy(src.name, "omnibuf-nano");
    src.raw.size = 4u;
    src.raw.bytes[0] = 0xCAu;
    src.raw.bytes[1] = 0xFEu;
    src.raw.bytes[2] = 0xBAu;
    src.raw.bytes[3] = 0xBEu;
    src.uuid[0] = 1u;
    src.uuid[1] = 2u;
    src.uuid[2] = 3u;
    src.uuid[3] = 4u;
    src.has_sub = true;
    src.sub.id = -77;
    strcpy(src.sub.label, "nested");
    src.nums_count = 3u;
    src.nums[0] = 10u;
    src.nums[1] = 200u;
    src.nums[2] = 300000u;
    src.subs_count = 2u;
    src.subs[0].id = 1;
    strcpy(src.subs[0].label, "first");
    src.subs[1].id = -2;
    strcpy(src.subs[1].label, "second");
    src.kv_count = 2u;
    strcpy(src.kv[0].key, "alpha");
    src.kv[0].value = 100u;
    strcpy(src.kv[1].key, "beta");
    src.kv[1].value = 9999999999ULL;
    src.which_choice = 16u;
    strcpy(src.choice.choice_str, "chosen");
    src.has_opt_bool = true;
    src.opt_bool = false; /* Explicit false must survive roundtrip */
    src.fixed_arr[0] = 7u;
    src.fixed_arr[1] = 8u;
    src.fixed_arr[2] = 9u;

    size_t expected_size = 0;
    assert(ob_get_encoded_size(&expected_size, &AllMsg_msg, &src));
    assert(expected_size > 0u);

    uint8_t wire[1024];
    chunk_stream_state_t wst = {wire, 0u, sizeof(wire), 0u};
    ob_ostream_t os = {byte_write_cb, &wst, sizeof(wire), 0u, NULL};
    assert(ob_encode(&os, &AllMsg_msg, &src));
    assert(os.bytes_written == expected_size);
    assert(wst.pos == expected_size);

    AllMsg dst;
    memset(&dst, 0xAA, sizeof(dst)); /* Poison destination to verify zero-init */
    chunk_stream_state_t rst = {wire, 0u, os.bytes_written, 0u};
    ob_istream_t is = {byte_read_cb, &rst, os.bytes_written, NULL};
    assert(ob_decode(&is, &AllMsg_msg, &dst));
    assert(is.bytes_left == 0u);

    assert(dst.u32 == src.u32);
    assert(dst.s64 == src.s64);
    assert(dst.flag == src.flag);
    assert(dst.f32 == src.f32);
    assert(dst.f64 == src.f64);
    assert(dst.fix32 == src.fix32);
    assert(dst.sfix64 == src.sfix64);
    assert(strcmp(dst.name, src.name) == 0);
    assert(dst.raw.size == src.raw.size);
    assert(memcmp(dst.raw.bytes, src.raw.bytes, src.raw.size) == 0);
    assert(memcmp(dst.uuid, src.uuid, 4u) == 0);
    assert(dst.has_sub);
    assert(dst.sub.id == -77);
    assert(strcmp(dst.sub.label, "nested") == 0);
    assert(dst.nums_count == 3u);
    assert(dst.nums[0] == 10u && dst.nums[1] == 200u && dst.nums[2] == 300000u);
    assert(dst.subs_count == 2u);
    assert(dst.subs[0].id == 1 && strcmp(dst.subs[0].label, "first") == 0);
    assert(dst.subs[1].id == -2 && strcmp(dst.subs[1].label, "second") == 0);
    assert(dst.kv_count == 2u);
    assert(strcmp(dst.kv[0].key, "alpha") == 0 && dst.kv[0].value == 100u);
    assert(strcmp(dst.kv[1].key, "beta") == 0 && dst.kv[1].value == 9999999999ULL);
    assert(dst.which_choice == 16u);
    assert(strcmp(dst.choice.choice_str, "chosen") == 0);
    assert(dst.has_opt_bool);
    assert(dst.opt_bool == false);
    assert(dst.fixed_arr[0] == 7u && dst.fixed_arr[1] == 8u && dst.fixed_arr[2] == 9u);
}

static void test_skip_field0_and_unknowns(void) {
    uint8_t buf[256];
    ob_ostream_t os = ob_ostream_from_buffer(buf, sizeof(buf));

    /* Field #0 (SchemaMeta, WireSubMsg = 12) */
    assert(ob_write_tag(&os, 0u, OB_WIRE_SUBMSG));
    assert(ob_varint_encode(&os, 5u));
    const uint8_t fake_meta[5] = {0x01u, 0x02u, 0x03u, 0x04u, 0x05u};
    assert(ob_write(&os, fake_meta, 5u));

    /* Unknown fields of every wire type (tags 99..114) */
    assert(ob_write_tag(&os, 99u, OB_WIRE_UINT));
    assert(ob_varint_encode(&os, 123456789u));

    assert(ob_write_tag(&os, 100u, OB_WIRE_SINT));
    assert(ob_varint_encode(&os, ob_zigzag_encode(-42)));

    assert(ob_write_tag(&os, 101u, OB_WIRE_BOOL_FALSE));
    assert(ob_write_tag(&os, 102u, OB_WIRE_BOOL_TRUE));

    const uint8_t four[4] = {1, 2, 3, 4};
    const uint8_t eight[8] = {1, 2, 3, 4, 5, 6, 7, 8};
    assert(ob_write_tag(&os, 103u, OB_WIRE_FIXED32));
    assert(ob_write(&os, four, 4u));
    assert(ob_write_tag(&os, 104u, OB_WIRE_FIXED64));
    assert(ob_write(&os, eight, 8u));
    assert(ob_write_tag(&os, 105u, OB_WIRE_SFIXED32));
    assert(ob_write(&os, four, 4u));
    assert(ob_write_tag(&os, 106u, OB_WIRE_SFIXED64));
    assert(ob_write(&os, eight, 8u));
    assert(ob_write_tag(&os, 107u, OB_WIRE_FLOAT32));
    assert(ob_write(&os, four, 4u));
    assert(ob_write_tag(&os, 108u, OB_WIRE_FLOAT64));
    assert(ob_write(&os, eight, 8u));

    /* Known field tag 1 (id = 999) */
    assert(ob_write_tag(&os, 1u, OB_WIRE_SINT));
    assert(ob_varint_encode(&os, ob_zigzag_encode(999)));

    /* Length-delimited unknowns (larger than 32B scratch to test chunked skip loop) */
    uint8_t big[48];
    memset(big, 0x41, sizeof(big));
    assert(ob_write_tag(&os, 109u, OB_WIRE_STRING));
    assert(ob_varint_encode(&os, sizeof(big)));
    assert(ob_write(&os, big, sizeof(big)));

    assert(ob_write_tag(&os, 110u, OB_WIRE_BYTES));
    assert(ob_varint_encode(&os, 3u));
    assert(ob_write(&os, four, 3u));

    assert(ob_write_tag(&os, 111u, OB_WIRE_EXTENDED));
    assert(ob_varint_encode(&os, 4u));
    assert(ob_write(&os, four, 4u));

    /* Known field tag 2 (label = "ok") */
    assert(ob_write_tag(&os, 2u, OB_WIRE_STRING));
    assert(ob_varint_encode(&os, 2u));
    assert(ob_write(&os, (const uint8_t *)"ok", 2u));

    SubMsg dst;
    ob_istream_t is = ob_istream_from_buffer(buf, os.bytes_written);
    assert(ob_decode(&is, &SubMsg_msg, &dst));
    assert(dst.id == 999);
    assert(strcmp(dst.label, "ok") == 0);
}

static void test_error_cases(void) {
    /* String overflow (32-byte buffer holds max 31 chars + NUL) */
    {
        uint8_t buf[64];
        ob_ostream_t os = ob_ostream_from_buffer(buf, sizeof(buf));
        assert(ob_write_tag(&os, 2u, OB_WIRE_STRING));
        assert(ob_varint_encode(&os, 32u));
        uint8_t s[32];
        memset(s, 'a', sizeof(s));
        assert(ob_write(&os, s, sizeof(s)));

        SubMsg dst;
        ob_istream_t is = ob_istream_from_buffer(buf, os.bytes_written);
        assert(!ob_decode(&is, &SubMsg_msg, &dst));
        assert(is.errmsg != NULL);
    }

    /* Invalid UTF-8 rejected on decode */
    {
        uint8_t buf[16];
        ob_ostream_t os = ob_ostream_from_buffer(buf, sizeof(buf));
        assert(ob_write_tag(&os, 2u, OB_WIRE_STRING));
        assert(ob_varint_encode(&os, 2u));
        const uint8_t bad[2] = {0xFFu, 0xFEu};
        assert(ob_write(&os, bad, 2u));

        SubMsg dst;
        ob_istream_t is = ob_istream_from_buffer(buf, os.bytes_written);
        assert(!ob_decode(&is, &SubMsg_msg, &dst));
        assert(is.errmsg != NULL);
    }

    /* Output buffer overflow */
    {
        SubMsg src = {12345, "hello world"};
        uint8_t tiny[4];
        ob_ostream_t os = ob_ostream_from_buffer(tiny, sizeof(tiny));
        assert(!ob_encode(&os, &SubMsg_msg, &src));
        assert(os.errmsg != NULL);
    }

    /* Field #0 with non-SubMsg wire type must fail */
    {
        uint8_t buf[8];
        ob_ostream_t os = ob_ostream_from_buffer(buf, sizeof(buf));
        assert(ob_write_tag(&os, 0u, OB_WIRE_UINT));
        assert(ob_varint_encode(&os, 1u));

        SubMsg dst;
        ob_istream_t is = ob_istream_from_buffer(buf, os.bytes_written);
        assert(!ob_decode(&is, &SubMsg_msg, &dst));
    }

    /* Embedded NUL in WireString must be rejected */
    {
        uint8_t buf[16];
        ob_ostream_t os = ob_ostream_from_buffer(buf, sizeof(buf));
        assert(ob_write_tag(&os, 2u, OB_WIRE_STRING));
        assert(ob_varint_encode(&os, 5u));
        const uint8_t nul_str[5] = {'a', 'b', '\0', 'c', 'd'};
        assert(ob_write(&os, nul_str, 5u));

        SubMsg dst;
        ob_istream_t is = ob_istream_from_buffer(buf, os.bytes_written);
        assert(!ob_decode(&is, &SubMsg_msg, &dst));
    }

    /* ob_decode_noinit with corrupted pre-existing array/map count > max_count must not underflow */
    {
        uint8_t buf[16];
        ob_ostream_t os = ob_ostream_from_buffer(buf, sizeof(buf));
        assert(ob_write_tag(&os, 12u, OB_WIRE_ARRAY));
        assert(ob_varint_encode(&os, 3u));
        const uint8_t body[3] = {OB_WIRE_UINT, 0x03u, 0x03u}; /* elem=UINT, count=1, val=1 */
        assert(ob_write(&os, body, 3u));

        AllMsg dst;
        memset(&dst, 0, sizeof(dst));
        dst.nums_count = 999u; /* Exceeds array_size (8) */
        ob_istream_t is = ob_istream_from_buffer(buf, os.bytes_written);
        assert(!ob_decode_noinit(&is, &AllMsg_msg, &dst));
    }

    /* Bogus WireArray count > remaining bytes must be rejected before touching dst */
    {
        uint8_t buf[16];
        ob_ostream_t os = ob_ostream_from_buffer(buf, sizeof(buf));
        assert(ob_write_tag(&os, 12u, OB_WIRE_ARRAY));
        assert(ob_varint_encode(&os, 3u));
        const uint8_t body[3] = {OB_WIRE_UINT, 0x0Fu, 0x03u}; /* count=7, only 1 byte left */
        assert(ob_write(&os, body, 3u));

        AllMsg dst;
        ob_istream_t is = ob_istream_from_buffer(buf, os.bytes_written);
        assert(!ob_decode(&is, &AllMsg_msg, &dst));
    }

    /* Bogus WireMap count > remaining bytes / 2 must be rejected */
    {
        uint8_t buf[16];
        ob_ostream_t os = ob_ostream_from_buffer(buf, sizeof(buf));
        assert(ob_write_tag(&os, 14u, OB_WIRE_MAP));
        assert(ob_varint_encode(&os, 4u));
        const uint8_t body[4] = {OB_WIRE_STRING, OB_WIRE_UINT, 0x05u, 0x01u}; /* count=2, 1 byte left */
        assert(ob_write(&os, body, 4u));

        AllMsg dst;
        ob_istream_t is = ob_istream_from_buffer(buf, os.bytes_written);
        assert(!ob_decode(&is, &AllMsg_msg, &dst));
    }

    /* ob_write size_t wraparound and bytes_written > max_size must fail cleanly */
    {
        ob_ostream_t sizing = {NULL, NULL, SIZE_MAX, SIZE_MAX - 2u, NULL};
        assert(!ob_write(&sizing, NULL, 4u));
        assert(sizing.errmsg != NULL);

        uint8_t dummy[4];
        ob_ostream_t bad = ob_ostream_from_buffer(dummy, 2u);
        bad.bytes_written = 10u;
        assert(!ob_write(&bad, dummy, 1u));
        assert(bad.errmsg != NULL);
    }

    /* ob_ostream_from_buffer(buf, 0) must fail with stream full, never overflow */
    {
        uint8_t canary[4] = {0xDEu, 0xADu, 0xBEu, 0xEFu};
        SubMsg src = {42, "hello"};
        ob_ostream_t os = ob_ostream_from_buffer(canary, 0u);
        assert(!ob_encode(&os, &SubMsg_msg, &src));
        assert(os.errmsg != NULL);
        assert(os.bytes_written == 0u);
        assert(canary[0] == 0xDEu && canary[1] == 0xADu &&
               canary[2] == 0xBEu && canary[3] == 0xEFu);

        /* Empty message encodes 0 bytes and succeeds even into a 0-byte buffer */
        SubMsg empty = {0, ""};
        ob_ostream_t os_empty = ob_ostream_from_buffer(NULL, 0u);
        assert(ob_encode(&os_empty, &SubMsg_msg, &empty));
        assert(os_empty.bytes_written == 0u);
    }

    /* Wrong wire type on a REQUIRED field must not satisfy the required check */
    {
        typedef struct { int32_t req_id; } ReqMsg;
        static const ob_field_t req_fields[] = {
            {1u, OB_WIRE_SINT, OB_FLAG_REQUIRED, offsetof(ReqMsg, req_id), 0u, sizeof(int32_t), 1u, NULL}
        };
        static const ob_msgdesc_t req_desc = {req_fields, 1u, sizeof(ReqMsg)};

        uint8_t buf[8];
        ob_ostream_t os = ob_ostream_from_buffer(buf, sizeof(buf));
        assert(ob_write_tag(&os, 1u, OB_WIRE_BOOL_TRUE)); /* wrong wire type for tag 1 */

        ReqMsg dst;
        ob_istream_t is = ob_istream_from_buffer(buf, os.bytes_written);
        assert(!ob_decode(&is, &req_desc, &dst));
    }

    /* Required field at descriptor index >= 256 must be rejected instead of skipped */
    {
        typedef struct { int32_t v; } BigMsg;
        ob_field_t fields[257];
        for (uint16_t i = 0u; i < 257u; i++) {
            fields[i].tag = (uint16_t)(i + 1u);
            fields[i].wire_type = OB_WIRE_SINT;
            fields[i].flags = (i == 256u) ? OB_FLAG_REQUIRED : OB_FLAG_SINGULAR;
            fields[i].data_offset = 0u;
            fields[i].size_offset = 0u;
            fields[i].data_size = sizeof(int32_t);
            fields[i].array_size = 1u;
            fields[i].submsg = NULL;
        }
        ob_msgdesc_t big_desc = {fields, 257u, sizeof(BigMsg)};
        BigMsg dst;
        ob_istream_t is = ob_istream_from_buffer(NULL, 0u);
        assert(!ob_decode(&is, &big_desc, &dst));
        assert(is.errmsg != NULL);
    }
}

static void write_str_u64_map_entry(ob_ostream_t *os, const char *k, uint64_t v) {
    size_t klen = strlen(k);
    assert(ob_varint_encode(os, (uint64_t)klen));
    assert(ob_write(os, (const uint8_t *)k, klen));
    assert(ob_varint_encode(os, v));
}

static void test_map_duplicate_keys(void) {
    /* 1. Duplicate key when map is below max_count: overwrites in place, count stays 1 */
    {
        uint8_t body[64];
        ob_ostream_t bs = ob_ostream_from_buffer(body, sizeof(body));
        const uint8_t hdr[2] = {OB_WIRE_STRING, OB_WIRE_UINT};
        assert(ob_write(&bs, hdr, 2u));
        assert(ob_varint_encode(&bs, 2u));
        write_str_u64_map_entry(&bs, "dup", 10u);
        write_str_u64_map_entry(&bs, "dup", 99u);

        uint8_t wire[128];
        ob_ostream_t os = ob_ostream_from_buffer(wire, sizeof(wire));
        assert(ob_write_tag(&os, 14u, OB_WIRE_MAP));
        assert(ob_varint_encode(&os, (uint64_t)bs.bytes_written));
        assert(ob_write(&os, body, bs.bytes_written));

        AllMsg dst;
        ob_istream_t is = ob_istream_from_buffer(wire, os.bytes_written);
        assert(ob_decode(&is, &AllMsg_msg, &dst));
        assert(dst.kv_count == 1u);
        assert(strcmp(dst.kv[0].key, "dup") == 0);
        assert(dst.kv[0].value == 99u);
    }

    /* 2. 5 wire entries with 4 distinct keys into max_count=4 map: last-write-wins without overflow */
    {
        uint8_t body[128];
        ob_ostream_t bs = ob_ostream_from_buffer(body, sizeof(body));
        const uint8_t hdr[2] = {OB_WIRE_STRING, OB_WIRE_UINT};
        assert(ob_write(&bs, hdr, 2u));
        assert(ob_varint_encode(&bs, 5u));
        write_str_u64_map_entry(&bs, "k1", 1u);
        write_str_u64_map_entry(&bs, "k2", 2u);
        write_str_u64_map_entry(&bs, "k3", 3u);
        write_str_u64_map_entry(&bs, "k4", 4u);
        write_str_u64_map_entry(&bs, "k2", 200u);

        uint8_t wire[256];
        ob_ostream_t os = ob_ostream_from_buffer(wire, sizeof(wire));
        assert(ob_write_tag(&os, 14u, OB_WIRE_MAP));
        assert(ob_varint_encode(&os, (uint64_t)bs.bytes_written));
        assert(ob_write(&os, body, bs.bytes_written));

        /* Also append a second WireMap updating "k4" when already at max_count */
        uint8_t body2[64];
        ob_ostream_t bs2 = ob_ostream_from_buffer(body2, sizeof(body2));
        assert(ob_write(&bs2, hdr, 2u));
        assert(ob_varint_encode(&bs2, 1u));
        write_str_u64_map_entry(&bs2, "k4", 400u);
        assert(ob_write_tag(&os, 14u, OB_WIRE_MAP));
        assert(ob_varint_encode(&os, (uint64_t)bs2.bytes_written));
        assert(ob_write(&os, body2, bs2.bytes_written));

        AllMsg dst;
        ob_istream_t is = ob_istream_from_buffer(wire, os.bytes_written);
        assert(ob_decode(&is, &AllMsg_msg, &dst));
        assert(dst.kv_count == 4u);
        assert(strcmp(dst.kv[0].key, "k1") == 0 && dst.kv[0].value == 1u);
        assert(strcmp(dst.kv[1].key, "k2") == 0 && dst.kv[1].value == 200u);
        assert(strcmp(dst.kv[2].key, "k3") == 0 && dst.kv[2].value == 3u);
        assert(strcmp(dst.kv[3].key, "k4") == 0 && dst.kv[3].value == 400u);
    }

    /* 3. 5 distinct keys into max_count=4 map must still fail */
    {
        uint8_t body[128];
        ob_ostream_t bs = ob_ostream_from_buffer(body, sizeof(body));
        const uint8_t hdr[2] = {OB_WIRE_STRING, OB_WIRE_UINT};
        assert(ob_write(&bs, hdr, 2u));
        assert(ob_varint_encode(&bs, 5u));
        write_str_u64_map_entry(&bs, "k1", 1u);
        write_str_u64_map_entry(&bs, "k2", 2u);
        write_str_u64_map_entry(&bs, "k3", 3u);
        write_str_u64_map_entry(&bs, "k4", 4u);
        write_str_u64_map_entry(&bs, "k5", 5u);

        uint8_t wire[256];
        ob_ostream_t os = ob_ostream_from_buffer(wire, sizeof(wire));
        assert(ob_write_tag(&os, 14u, OB_WIRE_MAP));
        assert(ob_varint_encode(&os, (uint64_t)bs.bytes_written));
        assert(ob_write(&os, body, bs.bytes_written));

        AllMsg dst;
        ob_istream_t is = ob_istream_from_buffer(wire, os.bytes_written);
        assert(!ob_decode(&is, &AllMsg_msg, &dst));
        assert(is.errmsg != NULL);
    }

    /* 4. Duplicate integer keys at max_count=2 */
    {
        typedef struct {
            int32_t key;
            uint32_t value;
        } IntMapEntry;
        static const ob_field_t ime_fields[] = {
            {1u, OB_WIRE_SINT, OB_FLAG_SINGULAR, offsetof(IntMapEntry, key),   0u, sizeof(int32_t),  1u, NULL},
            {2u, OB_WIRE_UINT, OB_FLAG_SINGULAR, offsetof(IntMapEntry, value), 0u, sizeof(uint32_t), 1u, NULL}
        };
        static const ob_msgdesc_t ime_desc = {ime_fields, 2u, sizeof(IntMapEntry)};

        typedef struct {
            uint16_t m_count;
            IntMapEntry m[2];
        } IntMapMsg;
        static const ob_field_t imm_fields[] = {
            {1u, OB_WIRE_MAP, OB_FLAG_MAP, offsetof(IntMapMsg, m), offsetof(IntMapMsg, m_count), sizeof(IntMapEntry), 2u, &ime_desc}
        };
        static const ob_msgdesc_t imm_desc = {imm_fields, 1u, sizeof(IntMapMsg)};

        uint8_t body[64];
        ob_ostream_t bs = ob_ostream_from_buffer(body, sizeof(body));
        const uint8_t hdr[2] = {OB_WIRE_SINT, OB_WIRE_UINT};
        assert(ob_write(&bs, hdr, 2u));
        assert(ob_varint_encode(&bs, 3u));
        assert(ob_varint_encode(&bs, ob_zigzag_encode(-5)));
        assert(ob_varint_encode(&bs, 11u));
        assert(ob_varint_encode(&bs, ob_zigzag_encode(7)));
        assert(ob_varint_encode(&bs, 22u));
        assert(ob_varint_encode(&bs, ob_zigzag_encode(-5)));
        assert(ob_varint_encode(&bs, 33u));

        uint8_t wire[128];
        ob_ostream_t os = ob_ostream_from_buffer(wire, sizeof(wire));
        assert(ob_write_tag(&os, 1u, OB_WIRE_MAP));
        assert(ob_varint_encode(&os, (uint64_t)bs.bytes_written));
        assert(ob_write(&os, body, bs.bytes_written));

        IntMapMsg dst;
        ob_istream_t is = ob_istream_from_buffer(wire, os.bytes_written);
        assert(ob_decode(&is, &imm_desc, &dst));
        assert(dst.m_count == 2u);
        assert(dst.m[0].key == -5 && dst.m[0].value == 33u);
        assert(dst.m[1].key == 7 && dst.m[1].value == 22u);
    }

    /* 5. Large (>= 256 byte) string map key at max_count=1:
     *    - duplicate valid UTF-8 key (multi-byte char straddling 64-byte boundary) updates in place
     *    - invalid UTF-8 and embedded NUL are rejected consistently with the < 256 path */
    {
        typedef struct {
            char key[300];
            uint32_t value;
        } BigKeyEntry;
        static const ob_field_t bke_fields[] = {
            {1u, OB_WIRE_STRING, OB_FLAG_SINGULAR, offsetof(BigKeyEntry, key),   0u, 300u,             1u, NULL},
            {2u, OB_WIRE_UINT,   OB_FLAG_SINGULAR, offsetof(BigKeyEntry, value), 0u, sizeof(uint32_t), 1u, NULL}
        };
        static const ob_msgdesc_t bke_desc = {bke_fields, 2u, sizeof(BigKeyEntry)};

        typedef struct {
            uint16_t m_count;
            BigKeyEntry m[1];
        } BigKeyMsg;
        static const ob_field_t bkm_fields[] = {
            {1u, OB_WIRE_MAP, OB_FLAG_MAP, offsetof(BigKeyMsg, m), offsetof(BigKeyMsg, m_count), sizeof(BigKeyEntry), 1u, &bke_desc}
        };
        static const ob_msgdesc_t bkm_desc = {bkm_fields, 1u, sizeof(BigKeyMsg)};

        /* 260-byte valid UTF-8 key with a 3-byte UTF-8 sequence straddling offset 64 (bytes 63..65). */
        uint8_t valid_key[260];
        memset(valid_key, 'a', sizeof(valid_key));
        valid_key[63] = 0xE2u;
        valid_key[64] = 0x82u;
        valid_key[65] = 0xACu; /* U+20AC EURO SIGN */

        uint8_t body[700];
        ob_ostream_t bs = ob_ostream_from_buffer(body, sizeof(body));
        const uint8_t hdr[2] = {OB_WIRE_STRING, OB_WIRE_UINT};
        assert(ob_write(&bs, hdr, 2u));
        assert(ob_varint_encode(&bs, 2u));
        assert(ob_varint_encode(&bs, sizeof(valid_key)));
        assert(ob_write(&bs, valid_key, sizeof(valid_key)));
        assert(ob_varint_encode(&bs, 10u));
        assert(ob_varint_encode(&bs, sizeof(valid_key)));
        assert(ob_write(&bs, valid_key, sizeof(valid_key)));
        assert(ob_varint_encode(&bs, 99u));

        uint8_t wire[768];
        ob_ostream_t os = ob_ostream_from_buffer(wire, sizeof(wire));
        assert(ob_write_tag(&os, 1u, OB_WIRE_MAP));
        assert(ob_varint_encode(&os, (uint64_t)bs.bytes_written));
        assert(ob_write(&os, body, bs.bytes_written));

        BigKeyMsg dst;
        ob_istream_t is = ob_istream_from_buffer(wire, os.bytes_written);
        assert(ob_decode(&is, &bkm_desc, &dst));
        assert(dst.m_count == 1u);
        assert(dst.m[0].value == 99u);
        assert(memcmp(dst.m[0].key, valid_key, sizeof(valid_key)) == 0);

        /* Second entry >= 256 bytes with invalid UTF-8 must fail with UTF-8 error */
        uint8_t bad_utf8[260];
        memcpy(bad_utf8, valid_key, sizeof(bad_utf8));
        bad_utf8[200] = 0xFFu;
        bs = ob_ostream_from_buffer(body, sizeof(body));
        assert(ob_write(&bs, hdr, 2u));
        assert(ob_varint_encode(&bs, 2u));
        assert(ob_varint_encode(&bs, sizeof(valid_key)));
        assert(ob_write(&bs, valid_key, sizeof(valid_key)));
        assert(ob_varint_encode(&bs, 10u));
        assert(ob_varint_encode(&bs, sizeof(bad_utf8)));
        assert(ob_write(&bs, bad_utf8, sizeof(bad_utf8)));
        assert(ob_varint_encode(&bs, 20u));
        os = ob_ostream_from_buffer(wire, sizeof(wire));
        assert(ob_write_tag(&os, 1u, OB_WIRE_MAP));
        assert(ob_varint_encode(&os, (uint64_t)bs.bytes_written));
        assert(ob_write(&os, body, bs.bytes_written));
        is = ob_istream_from_buffer(wire, os.bytes_written);
        assert(!ob_decode(&is, &bkm_desc, &dst));
        assert(is.errmsg != NULL && strcmp(is.errmsg, "invalid UTF-8 in string") == 0);

        /* Second entry >= 256 bytes with embedded NUL must fail with NUL error */
        uint8_t bad_nul[260];
        memcpy(bad_nul, valid_key, sizeof(bad_nul));
        bad_nul[150] = '\0';
        bs = ob_ostream_from_buffer(body, sizeof(body));
        assert(ob_write(&bs, hdr, 2u));
        assert(ob_varint_encode(&bs, 2u));
        assert(ob_varint_encode(&bs, sizeof(valid_key)));
        assert(ob_write(&bs, valid_key, sizeof(valid_key)));
        assert(ob_varint_encode(&bs, 10u));
        assert(ob_varint_encode(&bs, sizeof(bad_nul)));
        assert(ob_write(&bs, bad_nul, sizeof(bad_nul)));
        assert(ob_varint_encode(&bs, 20u));
        os = ob_ostream_from_buffer(wire, sizeof(wire));
        assert(ob_write_tag(&os, 1u, OB_WIRE_MAP));
        assert(ob_varint_encode(&os, (uint64_t)bs.bytes_written));
        assert(ob_write(&os, body, bs.bytes_written));
        is = ob_istream_from_buffer(wire, os.bytes_written);
        assert(!ob_decode(&is, &bkm_desc, &dst));
        assert(is.errmsg != NULL && strcmp(is.errmsg, "embedded NUL in string") == 0);
    }
}

static void test_optional_explicit_presence_all_types(void) {
    typedef int32_t TestOpenEnum;
    typedef struct {
        bool has_i32;
        int32_t i32;
        bool has_i64;
        int64_t i64;
        bool has_u32;
        uint32_t u32;
        bool has_u64;
        uint64_t u64;
        bool has_s32;
        int32_t s32;
        bool has_s64;
        int64_t s64;
        bool has_f32;
        uint32_t f32;
        bool has_f64;
        uint64_t f64;
        bool has_sf32;
        int32_t sf32;
        bool has_sf64;
        int64_t sf64;
        bool has_fl;
        float fl;
        bool has_db;
        double db;
        bool has_b;
        bool b;
        bool has_en;
        TestOpenEnum en;
        bool has_str;
        char str[16];
        bool has_raw;
        OB_BYTES_ARRAY_T(16) raw;
        bool has_fixraw;
        uint8_t fixraw[4];
        bool has_sub;
        SubMsg sub;
        uint16_t which_o;
        union {
            int32_t o_i32;
            bool o_bool;
            char o_str[16];
            OB_BYTES_ARRAY_T(8) o_raw;
            SubMsg o_sub;
        } o;
    } OptAllMsg;

    static const ob_field_t opt_fields[] = {
        {1u,  OB_WIRE_SINT,       OB_FLAG_OPTIONAL, offsetof(OptAllMsg, i32),    offsetof(OptAllMsg, has_i32),    sizeof(int32_t),      1u, NULL},
        {2u,  OB_WIRE_SINT,       OB_FLAG_OPTIONAL, offsetof(OptAllMsg, i64),    offsetof(OptAllMsg, has_i64),    sizeof(int64_t),      1u, NULL},
        {3u,  OB_WIRE_UINT,       OB_FLAG_OPTIONAL, offsetof(OptAllMsg, u32),    offsetof(OptAllMsg, has_u32),    sizeof(uint32_t),     1u, NULL},
        {4u,  OB_WIRE_UINT,       OB_FLAG_OPTIONAL, offsetof(OptAllMsg, u64),    offsetof(OptAllMsg, has_u64),    sizeof(uint64_t),     1u, NULL},
        {5u,  OB_WIRE_SINT,       OB_FLAG_OPTIONAL, offsetof(OptAllMsg, s32),    offsetof(OptAllMsg, has_s32),    sizeof(int32_t),      1u, NULL},
        {6u,  OB_WIRE_SINT,       OB_FLAG_OPTIONAL, offsetof(OptAllMsg, s64),    offsetof(OptAllMsg, has_s64),    sizeof(int64_t),      1u, NULL},
        {7u,  OB_WIRE_FIXED32,    OB_FLAG_OPTIONAL, offsetof(OptAllMsg, f32),    offsetof(OptAllMsg, has_f32),    sizeof(uint32_t),     1u, NULL},
        {8u,  OB_WIRE_FIXED64,    OB_FLAG_OPTIONAL, offsetof(OptAllMsg, f64),    offsetof(OptAllMsg, has_f64),    sizeof(uint64_t),     1u, NULL},
        {9u,  OB_WIRE_SFIXED32,   OB_FLAG_OPTIONAL, offsetof(OptAllMsg, sf32),   offsetof(OptAllMsg, has_sf32),   sizeof(int32_t),      1u, NULL},
        {10u, OB_WIRE_SFIXED64,   OB_FLAG_OPTIONAL, offsetof(OptAllMsg, sf64),   offsetof(OptAllMsg, has_sf64),   sizeof(int64_t),      1u, NULL},
        {11u, OB_WIRE_FLOAT32,    OB_FLAG_OPTIONAL, offsetof(OptAllMsg, fl),     offsetof(OptAllMsg, has_fl),     sizeof(float),        1u, NULL},
        {12u, OB_WIRE_FLOAT64,    OB_FLAG_OPTIONAL, offsetof(OptAllMsg, db),     offsetof(OptAllMsg, has_db),     sizeof(double),       1u, NULL},
        {13u, OB_WIRE_BOOL_FALSE, OB_FLAG_OPTIONAL, offsetof(OptAllMsg, b),      offsetof(OptAllMsg, has_b),      sizeof(bool),         1u, NULL},
        {14u, OB_WIRE_SINT,       OB_FLAG_OPTIONAL, offsetof(OptAllMsg, en),     offsetof(OptAllMsg, has_en),     sizeof(TestOpenEnum), 1u, NULL},
        {15u, OB_WIRE_STRING,     OB_FLAG_OPTIONAL, offsetof(OptAllMsg, str),    offsetof(OptAllMsg, has_str),    16u,                  1u, NULL},
        {16u, OB_WIRE_BYTES,      OB_FLAG_OPTIONAL, offsetof(OptAllMsg, raw),    offsetof(OptAllMsg, has_raw),    16u,                  1u, NULL},
        {17u, OB_WIRE_BYTES,      OB_FLAG_OPTIONAL | OB_FLAG_FIXED_LEN, offsetof(OptAllMsg, fixraw), offsetof(OptAllMsg, has_fixraw), 4u, 1u, NULL},
        {18u, OB_WIRE_SUBMSG,     OB_FLAG_OPTIONAL, offsetof(OptAllMsg, sub),    offsetof(OptAllMsg, has_sub),    sizeof(SubMsg),       1u, &SubMsg_msg},
        {19u, OB_WIRE_SINT,       OB_FLAG_ONEOF,    offsetof(OptAllMsg, o.o_i32),  offsetof(OptAllMsg, which_o),  sizeof(int32_t),      1u, NULL},
        {20u, OB_WIRE_BOOL_FALSE, OB_FLAG_ONEOF,    offsetof(OptAllMsg, o.o_bool), offsetof(OptAllMsg, which_o),  sizeof(bool),         1u, NULL},
        {21u, OB_WIRE_STRING,     OB_FLAG_ONEOF,    offsetof(OptAllMsg, o.o_str),  offsetof(OptAllMsg, which_o),  16u,                  1u, NULL},
        {22u, OB_WIRE_BYTES,      OB_FLAG_ONEOF,    offsetof(OptAllMsg, o.o_raw),  offsetof(OptAllMsg, which_o),  8u,                   1u, NULL},
        {23u, OB_WIRE_SUBMSG,     OB_FLAG_ONEOF,    offsetof(OptAllMsg, o.o_sub),  offsetof(OptAllMsg, which_o),  sizeof(SubMsg),       1u, &SubMsg_msg}
    };
    static const ob_msgdesc_t opt_desc = {opt_fields, 23u, sizeof(OptAllMsg)};

    /* 1. All absent -> 0 wire bytes */
    {
        OptAllMsg src;
        memset(&src, 0, sizeof(src));
        uint8_t wire[256];
        ob_ostream_t os = ob_ostream_from_buffer(wire, sizeof(wire));
        assert(ob_encode(&os, &opt_desc, &src));
        assert(os.bytes_written == 0u);

        OptAllMsg dst;
        memset(&dst, 0xAA, sizeof(dst));
        ob_istream_t is = ob_istream_from_buffer(wire, os.bytes_written);
        assert(ob_decode(&is, &opt_desc, &dst));
        assert(!dst.has_i32 && !dst.has_i64 && !dst.has_u32 && !dst.has_u64);
        assert(!dst.has_s32 && !dst.has_s64 && !dst.has_f32 && !dst.has_f64);
        assert(!dst.has_sf32 && !dst.has_sf64 && !dst.has_fl && !dst.has_db);
        assert(!dst.has_b && !dst.has_en && !dst.has_str && !dst.has_raw);
        assert(!dst.has_fixraw && !dst.has_sub && dst.which_o == 0u);
    }

    /* 2. All present with explicit zero/false/empty values -> every field encoded & preserved */
    {
        OptAllMsg src;
        memset(&src, 0, sizeof(src));
        src.has_i32 = true;
        src.has_i64 = true;
        src.has_u32 = true;
        src.has_u64 = true;
        src.has_s32 = true;
        src.has_s64 = true;
        src.has_f32 = true;
        src.has_f64 = true;
        src.has_sf32 = true;
        src.has_sf64 = true;
        src.has_fl = true;
        src.has_db = true;
        src.has_b = true;
        src.b = false;
        src.has_en = true;
        src.en = 0;
        src.has_str = true;
        src.str[0] = '\0';
        src.has_raw = true;
        src.raw.size = 0u;
        src.has_fixraw = true;
        src.has_sub = true;
        src.which_o = 20u;
        src.o.o_bool = false;

        size_t expected_size = 0u;
        assert(ob_get_encoded_size(&expected_size, &opt_desc, &src));
        assert(expected_size > 0u);

        uint8_t wire[512];
        chunk_stream_state_t wst = {wire, 0u, sizeof(wire), 0u};
        ob_ostream_t os = {byte_write_cb, &wst, sizeof(wire), 0u, NULL};
        assert(ob_encode(&os, &opt_desc, &src));
        assert(os.bytes_written == expected_size);

        OptAllMsg dst;
        memset(&dst, 0xAA, sizeof(dst));
        ob_istream_t is = ob_istream_from_buffer(wire, os.bytes_written);
        assert(ob_decode(&is, &opt_desc, &dst));
        assert(is.bytes_left == 0u);

        assert(dst.has_i32 && dst.i32 == 0);
        assert(dst.has_i64 && dst.i64 == 0);
        assert(dst.has_u32 && dst.u32 == 0u);
        assert(dst.has_u64 && dst.u64 == 0u);
        assert(dst.has_s32 && dst.s32 == 0);
        assert(dst.has_s64 && dst.s64 == 0);
        assert(dst.has_f32 && dst.f32 == 0u);
        assert(dst.has_f64 && dst.f64 == 0u);
        assert(dst.has_sf32 && dst.sf32 == 0);
        assert(dst.has_sf64 && dst.sf64 == 0);
        assert(dst.has_fl && dst.fl == 0.0f);
        assert(dst.has_db && dst.db == 0.0);
        assert(dst.has_b && dst.b == false);
        assert(dst.has_en && dst.en == 0);
        assert(dst.has_str && dst.str[0] == '\0');
        assert(dst.has_raw && dst.raw.size == 0u);
        assert(dst.has_fixraw && dst.fixraw[0] == 0u && dst.fixraw[3] == 0u);
        assert(dst.has_sub && dst.sub.id == 0 && dst.sub.label[0] == '\0');
        assert(dst.which_o == 20u && dst.o.o_bool == false);
    }

    /* 3. Explicit bool=false encodes OB_WIRE_BOOL_FALSE tag with 0 payload bytes */
    {
        OptAllMsg src;
        memset(&src, 0, sizeof(src));
        src.has_b = true;
        src.b = false;

        uint8_t wire[16];
        ob_ostream_t os = ob_ostream_from_buffer(wire, sizeof(wire));
        assert(ob_encode(&os, &opt_desc, &src));
        /* tag=13, wt=OB_WIRE_BOOL_FALSE(2) -> raw=(13<<4)|2=210 -> 2-byte varint ((210<<2)|2)=0x034A */
        assert(os.bytes_written == 2u);
        assert(wire[0] == 0x4Au && wire[1] == 0x03u);
    }

    /* 4. Oneof active zero/empty arms & branch switching on wire */
    {
        const uint16_t zero_tags[] = {19u, 20u, 21u, 22u, 23u};
        for (size_t i = 0; i < sizeof(zero_tags) / sizeof(zero_tags[0]); i++) {
            OptAllMsg src;
            memset(&src, 0, sizeof(src));
            src.which_o = zero_tags[i];

            uint8_t wire[64];
            ob_ostream_t os = ob_ostream_from_buffer(wire, sizeof(wire));
            assert(ob_encode(&os, &opt_desc, &src));
            assert(os.bytes_written > 0u);

            OptAllMsg dst;
            memset(&dst, 0xAA, sizeof(dst));
            ob_istream_t is = ob_istream_from_buffer(wire, os.bytes_written);
            assert(ob_decode(&is, &opt_desc, &dst));
            assert(dst.which_o == zero_tags[i]);
        }

        /* Wire stream switching oneof branches: str("hello") -> bool(false) -> sub({label:"ok"}) -> i32(0) */
        uint8_t wire[128];
        ob_ostream_t os = ob_ostream_from_buffer(wire, sizeof(wire));
        assert(ob_write_tag(&os, 21u, OB_WIRE_STRING));
        assert(ob_varint_encode(&os, 5u));
        assert(ob_write(&os, (const uint8_t *)"hello", 5u));
        assert(ob_write_tag(&os, 20u, OB_WIRE_BOOL_FALSE));
        assert(ob_write_tag(&os, 23u, OB_WIRE_SUBMSG));
        assert(ob_varint_encode(&os, 4u));
        assert(ob_write_tag(&os, 2u, OB_WIRE_STRING));
        assert(ob_varint_encode(&os, 2u));
        assert(ob_write(&os, (const uint8_t *)"ok", 2u));

        OptAllMsg dst;
        ob_istream_t is = ob_istream_from_buffer(wire, os.bytes_written);
        assert(ob_decode(&is, &opt_desc, &dst));
        assert(dst.which_o == 23u);
        assert(dst.o.o_sub.id == 0);
        assert(strcmp(dst.o.o_sub.label, "ok") == 0);

        /* Append tag 19 (o_i32 = 0) to switch away from submsg */
        assert(ob_write_tag(&os, 19u, OB_WIRE_SINT));
        assert(ob_varint_encode(&os, 0u));
        is = ob_istream_from_buffer(wire, os.bytes_written);
        assert(ob_decode(&is, &opt_desc, &dst));
        assert(dst.which_o == 19u);
        assert(dst.o.o_i32 == 0);
    }

    /* 5. Open enum unknown positive & negative values preserved */
    {
        const TestOpenEnum vals[] = {0, 4242, -99};
        for (size_t i = 0; i < sizeof(vals) / sizeof(vals[0]); i++) {
            OptAllMsg src;
            memset(&src, 0, sizeof(src));
            src.has_en = true;
            src.en = vals[i];

            uint8_t wire[32];
            ob_ostream_t os = ob_ostream_from_buffer(wire, sizeof(wire));
            assert(ob_encode(&os, &opt_desc, &src));
            assert(os.bytes_written > 0u);

            OptAllMsg dst;
            ob_istream_t is = ob_istream_from_buffer(wire, os.bytes_written);
            assert(ob_decode(&is, &opt_desc, &dst));
            assert(dst.has_en && dst.en == vals[i]);
        }
    }
}

int main(void) {
    test_varint_boundaries();
    test_zigzag();
    test_utf8();
    test_full_roundtrip();
    test_skip_field0_and_unknowns();
    test_error_cases();
    test_map_duplicate_keys();
    test_optional_explicit_presence_all_types();
    printf("All C99 runtime unit tests passed!\n");
    return 0;
}
