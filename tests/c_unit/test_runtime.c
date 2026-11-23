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
}

int main(void) {
    test_varint_boundaries();
    test_zigzag();
    test_utf8();
    test_full_roundtrip();
    test_skip_field0_and_unknowns();
    test_error_cases();
    printf("All C99 runtime unit tests passed!\n");
    return 0;
}
