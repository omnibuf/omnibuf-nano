#include "ob_decode.h"
#include "ob_varint.h"

static bool buf_read_callback(ob_istream_t *stream, uint8_t *buf, size_t count) {
    const uint8_t *src = (const uint8_t *)stream->state;
    memcpy(buf, src, count);
    stream->state = (void *)(src + count);
    return true;
}

ob_istream_t ob_istream_from_buffer(const uint8_t *buf, size_t msglen) {
    ob_istream_t stream;
    stream.callback = buf_read_callback;
    stream.state = (void *)buf;
    stream.bytes_left = msglen;
    stream.errmsg = NULL;
    return stream;
}

bool ob_read(ob_istream_t *stream, uint8_t *buf, size_t count) {
    if (count == 0u) {
        return true;
    }
    if (stream->bytes_left < count) {
        OB_RETURN_ERROR(stream, "end-of-stream");
    }
    if (stream->callback == buf_read_callback) {
        const uint8_t *src = (const uint8_t *)stream->state;
        memcpy(buf, src, count);
        stream->state = (void *)(src + count);
    } else {
        if (stream->callback == NULL) {
            OB_RETURN_ERROR(stream, "null read callback");
        }
        if (!stream->callback(stream, buf, count)) {
            if (stream->errmsg == NULL) {
                stream->errmsg = "read callback failed";
            }
            return false;
        }
    }
    stream->bytes_left -= count;
    return true;
}

static bool ob_skip_bytes(ob_istream_t *stream, size_t count) {
    uint8_t scratch[32];
    if (stream->bytes_left < count) {
        OB_RETURN_ERROR(stream, "end-of-stream");
    }
    while (count > 0u) {
        size_t chunk = (count > sizeof(scratch)) ? sizeof(scratch) : count;
        if (!ob_read(stream, scratch, chunk)) {
            return false;
        }
        count -= chunk;
    }
    return true;
}

static bool read_le32(ob_istream_t *stream, uint32_t *out) {
    uint8_t b[4];
    if (!ob_read(stream, b, 4u)) {
        return false;
    }
    *out = (uint32_t)b[0]
         | ((uint32_t)b[1] << 8)
         | ((uint32_t)b[2] << 16)
         | ((uint32_t)b[3] << 24);
    return true;
}

static bool read_le64(ob_istream_t *stream, uint64_t *out) {
    uint8_t b[8];
    if (!ob_read(stream, b, 8u)) {
        return false;
    }
    *out = (uint64_t)b[0]
         | ((uint64_t)b[1] << 8)
         | ((uint64_t)b[2] << 16)
         | ((uint64_t)b[3] << 24)
         | ((uint64_t)b[4] << 32)
         | ((uint64_t)b[5] << 40)
         | ((uint64_t)b[6] << 48)
         | ((uint64_t)b[7] << 56);
    return true;
}

static bool ob_skip_value(ob_istream_t *stream, uint8_t wt) {
    switch (wt) {
    case OB_WIRE_UINT:
    case OB_WIRE_SINT: {
        uint64_t dummy = 0;
        return ob_varint_decode(stream, &dummy);
    }
    case OB_WIRE_BOOL_FALSE:
    case OB_WIRE_BOOL_TRUE:
        return true;
    case OB_WIRE_FIXED32:
    case OB_WIRE_SFIXED32:
    case OB_WIRE_FLOAT32:
        return ob_skip_bytes(stream, 4u);
    case OB_WIRE_FIXED64:
    case OB_WIRE_SFIXED64:
    case OB_WIRE_FLOAT64:
        return ob_skip_bytes(stream, 8u);
    case OB_WIRE_STRING:
    case OB_WIRE_BYTES:
    case OB_WIRE_SUBMSG:
    case OB_WIRE_ARRAY:
    case OB_WIRE_MAP:
    case OB_WIRE_EXTENDED: {
        uint64_t blen = 0;
        if (!ob_varint_decode(stream, &blen)) {
            return false;
        }
        if (blen > (uint64_t)stream->bytes_left) {
            OB_RETURN_ERROR(stream, "length exceeds stream");
        }
        return ob_skip_bytes(stream, (size_t)blen);
    }
    default:
        OB_RETURN_ERROR(stream, "invalid wire type");
    }
}

/* Binary search for tag in desc->fields (sorted ascending by tag). */
static const ob_field_t *find_field(const ob_msgdesc_t *desc, uint16_t tag, uint16_t *idx_out) {
    uint16_t lo = 0;
    uint16_t hi = desc->field_count;
    while (lo < hi) {
        uint16_t mid = (uint16_t)(lo + ((hi - lo) / 2u));
        uint16_t mtag = desc->fields[mid].tag;
        if (mtag == tag) {
            if (idx_out != NULL) {
                *idx_out = mid;
            }
            return &desc->fields[mid];
        }
        if (mtag < tag) {
            lo = (uint16_t)(mid + 1u);
        } else {
            hi = mid;
        }
    }
    return NULL;
}

static size_t repeated_elem_stride(const ob_field_t *field) {
    if (field->wire_type == OB_WIRE_BYTES && (field->flags & OB_FLAG_FIXED_LEN) == 0u) {
        return OB_BYTES_STRIDE(field->data_size);
    }
    return (size_t)field->data_size;
}

static bool decode_msg_depth(ob_istream_t *stream, const ob_msgdesc_t *desc,
                             void *dst_struct, unsigned int depth);

/* Decode a bounded substream of length sub_len into a sub-message. */
static bool decode_submsg_bounded(ob_istream_t *stream, const ob_msgdesc_t *subdesc,
                                  void *dst_sub, unsigned int depth) {
    uint64_t sub_len = 0;
    if (!ob_varint_decode(stream, &sub_len)) {
        return false;
    }
    if (sub_len > (uint64_t)stream->bytes_left) {
        OB_RETURN_ERROR(stream, "submessage length exceeds stream");
    }
    ob_istream_t sub = *stream;
    sub.bytes_left = (size_t)sub_len;
    if (!decode_msg_depth(&sub, subdesc, dst_sub, depth + 1u)) {
        stream->errmsg = sub.errmsg;
        return false;
    }
    if (sub.bytes_left != 0u) {
        OB_RETURN_ERROR(stream, "submessage trailing bytes");
    }
    stream->state = sub.state;
    stream->bytes_left -= (size_t)sub_len;
    return true;
}

/* Decode a single value payload (without field tag).
 * Note: For bool in container context, reads 1 byte (0 or 1). */
static bool decode_payload(ob_istream_t *stream, const ob_field_t *field,
                           void *ptr, unsigned int depth) {
    switch (field->wire_type) {
    case OB_WIRE_UINT: {
        uint64_t v = 0;
        if (!ob_varint_decode(stream, &v)) {
            return false;
        }
        if (field->data_size == 1u) {
            uint8_t v8 = (uint8_t)v;
            memcpy(ptr, &v8, sizeof(v8));
        } else if (field->data_size == 2u) {
            uint16_t v16 = (uint16_t)v;
            memcpy(ptr, &v16, sizeof(v16));
        } else if (field->data_size == 4u) {
            uint32_t v32 = (uint32_t)v;
            memcpy(ptr, &v32, sizeof(v32));
        } else {
            memcpy(ptr, &v, sizeof(v));
        }
        return true;
    }

    case OB_WIRE_SINT: {
        uint64_t raw = 0;
        if (!ob_varint_decode(stream, &raw)) {
            return false;
        }
        int64_t v = ob_zigzag_decode(raw);
        if (field->data_size == 1u) {
            int8_t v8 = (int8_t)v;
            memcpy(ptr, &v8, sizeof(v8));
        } else if (field->data_size == 2u) {
            int16_t v16 = (int16_t)v;
            memcpy(ptr, &v16, sizeof(v16));
        } else if (field->data_size == 4u) {
            int32_t v32 = (int32_t)v;
            memcpy(ptr, &v32, sizeof(v32));
        } else {
            memcpy(ptr, &v, sizeof(v));
        }
        return true;
    }

    case OB_WIRE_BOOL_FALSE: {
        uint8_t b = 0;
        if (!ob_read(stream, &b, 1u)) {
            return false;
        }
        if (b > 1u) {
            OB_RETURN_ERROR(stream, "invalid boolean value");
        }
        bool v = (b == 1u);
        memcpy(ptr, &v, sizeof(v));
        return true;
    }

    case OB_WIRE_FIXED32:
    case OB_WIRE_SFIXED32:
    case OB_WIRE_FLOAT32: {
        uint32_t bits = 0;
        if (!read_le32(stream, &bits)) {
            return false;
        }
        memcpy(ptr, &bits, sizeof(bits));
        return true;
    }

    case OB_WIRE_FIXED64:
    case OB_WIRE_SFIXED64:
    case OB_WIRE_FLOAT64: {
        uint64_t bits = 0;
        if (!read_le64(stream, &bits)) {
            return false;
        }
        memcpy(ptr, &bits, sizeof(bits));
        return true;
    }

    case OB_WIRE_STRING: {
        uint64_t slen = 0;
        if (!ob_varint_decode(stream, &slen)) {
            return false;
        }
        if (field->data_size == 0u || slen >= (uint64_t)field->data_size) {
            OB_RETURN_ERROR(stream, "string exceeds max_size");
        }
        if (slen > (uint64_t)stream->bytes_left) {
            OB_RETURN_ERROR(stream, "string length exceeds stream");
        }
        uint8_t *dst = (uint8_t *)ptr;
        if (!ob_read(stream, dst, (size_t)slen)) {
            return false;
        }
        dst[slen] = '\0';
        if (!ob_valid_utf8(dst, (size_t)slen)) {
            OB_RETURN_ERROR(stream, "invalid UTF-8 in string");
        }
        return true;
    }

    case OB_WIRE_BYTES: {
        uint64_t blen = 0;
        if (!ob_varint_decode(stream, &blen)) {
            return false;
        }
        if (blen > (uint64_t)stream->bytes_left) {
            OB_RETURN_ERROR(stream, "bytes length exceeds stream");
        }
        if ((field->flags & OB_FLAG_FIXED_LEN) != 0u) {
            if (blen != (uint64_t)field->data_size) {
                OB_RETURN_ERROR(stream, "fixed_length bytes size mismatch");
            }
            return ob_read(stream, (uint8_t *)ptr, (size_t)blen);
        } else {
            if (blen > (uint64_t)field->data_size) {
                OB_RETURN_ERROR(stream, "bytes exceeds max_size");
            }
            uint16_t sz = (uint16_t)blen;
            memcpy(ptr, &sz, sizeof(sz));
            return ob_read(stream, (uint8_t *)ptr + sizeof(uint16_t), (size_t)blen);
        }
    }

    case OB_WIRE_SUBMSG: {
        if (field->submsg == NULL) {
            OB_RETURN_ERROR(stream, "missing submsg descriptor");
        }
        return decode_submsg_bounded(stream, field->submsg, ptr, depth);
    }

    default:
        OB_RETURN_ERROR(stream, "unsupported wire type");
    }
}

static bool decode_array_field(ob_istream_t *stream, const ob_field_t *field,
                               uint8_t *base, unsigned int depth) {
    if (depth + 1u > OB_MAX_DEPTH) {
        OB_RETURN_ERROR(stream, "max nesting depth exceeded");
    }
    uint64_t body_len = 0;
    if (!ob_varint_decode(stream, &body_len)) {
        return false;
    }
    if (body_len < 2u || body_len > (uint64_t)stream->bytes_left) {
        OB_RETURN_ERROR(stream, "invalid array length");
    }

    ob_istream_t sub = *stream;
    sub.bytes_left = (size_t)body_len;

    uint8_t elem_wt = 0;
    if (!ob_read(&sub, &elem_wt, 1u)) {
        stream->errmsg = sub.errmsg;
        return false;
    }
    if (elem_wt != field->wire_type) {
        OB_RETURN_ERROR(stream, "array element wire type mismatch");
    }

    uint64_t count = 0;
    if (!ob_varint_decode(&sub, &count)) {
        stream->errmsg = sub.errmsg;
        return false;
    }

    uint8_t *arr_ptr = base + field->data_offset;
    size_t stride = repeated_elem_stride(field);

    if ((field->flags & OB_FLAG_FIXED_LEN) != 0u) {
        if (count != (uint64_t)field->array_size) {
            OB_RETURN_ERROR(stream, "fixed_length array count mismatch");
        }
        for (uint16_t i = 0; i < field->array_size; i++) {
            uint8_t *elem_ptr = arr_ptr + ((size_t)i * stride);
            if (field->wire_type == OB_WIRE_SUBMSG) {
                memset(elem_ptr, 0, stride);
            }
            if (!decode_payload(&sub, field, elem_ptr, depth + 1u)) {
                stream->errmsg = sub.errmsg;
                return false;
            }
        }
    } else {
        uint16_t existing = 0;
        memcpy(&existing, base + field->size_offset, sizeof(existing));
        if (count > (uint64_t)(field->array_size - existing)) {
            OB_RETURN_ERROR(stream, "array count exceeds max_count");
        }
        for (uint64_t i = 0; i < count; i++) {
            uint8_t *elem_ptr = arr_ptr + ((size_t)(existing + i) * stride);
            if (field->wire_type == OB_WIRE_SUBMSG) {
                memset(elem_ptr, 0, stride);
            }
            if (!decode_payload(&sub, field, elem_ptr, depth + 1u)) {
                stream->errmsg = sub.errmsg;
                return false;
            }
        }
        uint16_t new_count = (uint16_t)(existing + count);
        memcpy(base + field->size_offset, &new_count, sizeof(new_count));
    }

    if (sub.bytes_left != 0u) {
        OB_RETURN_ERROR(stream, "array trailing bytes");
    }
    stream->state = sub.state;
    stream->bytes_left -= (size_t)body_len;
    return true;
}

static bool decode_map_field(ob_istream_t *stream, const ob_field_t *field,
                             uint8_t *base, unsigned int depth) {
    if (depth + 1u > OB_MAX_DEPTH) {
        OB_RETURN_ERROR(stream, "max nesting depth exceeded");
    }
    const ob_msgdesc_t *entry_desc = field->submsg;
    if (entry_desc == NULL || entry_desc->field_count != 2u) {
        OB_RETURN_ERROR(stream, "invalid map entry descriptor");
    }
    const ob_field_t *kfield = &entry_desc->fields[0];
    const ob_field_t *vfield = &entry_desc->fields[1];

    uint64_t body_len = 0;
    if (!ob_varint_decode(stream, &body_len)) {
        return false;
    }
    if (body_len < 3u || body_len > (uint64_t)stream->bytes_left) {
        OB_RETURN_ERROR(stream, "invalid map length");
    }

    ob_istream_t sub = *stream;
    sub.bytes_left = (size_t)body_len;

    uint8_t header[2];
    if (!ob_read(&sub, header, 2u)) {
        stream->errmsg = sub.errmsg;
        return false;
    }
    if (header[0] != kfield->wire_type || header[1] != vfield->wire_type) {
        OB_RETURN_ERROR(stream, "map wire type mismatch");
    }

    uint64_t count = 0;
    if (!ob_varint_decode(&sub, &count)) {
        stream->errmsg = sub.errmsg;
        return false;
    }

    uint16_t existing = 0;
    memcpy(&existing, base + field->size_offset, sizeof(existing));
    if (count > (uint64_t)(field->array_size - existing)) {
        OB_RETURN_ERROR(stream, "map count exceeds max_count");
    }

    uint8_t *arr_ptr = base + field->data_offset;
    size_t stride = (size_t)field->data_size;
    for (uint64_t i = 0; i < count; i++) {
        uint8_t *entry = arr_ptr + ((size_t)(existing + i) * stride);
        memset(entry, 0, stride);
        if (!decode_payload(&sub, kfield, entry + kfield->data_offset, depth + 1u)) {
            stream->errmsg = sub.errmsg;
            return false;
        }
        if (!decode_payload(&sub, vfield, entry + vfield->data_offset, depth + 1u)) {
            stream->errmsg = sub.errmsg;
            return false;
        }
        if ((vfield->flags & OB_FLAG_OPTIONAL) != 0u) {
            bool has_val = true;
            memcpy(entry + vfield->size_offset, &has_val, sizeof(has_val));
        }
    }

    if (sub.bytes_left != 0u) {
        OB_RETURN_ERROR(stream, "map trailing bytes");
    }
    uint16_t new_count = (uint16_t)(existing + count);
    memcpy(base + field->size_offset, &new_count, sizeof(new_count));
    stream->state = sub.state;
    stream->bytes_left -= (size_t)body_len;
    return true;
}

static bool decode_msg_depth(ob_istream_t *stream, const ob_msgdesc_t *desc,
                             void *dst_struct, unsigned int depth) {
    if (depth > OB_MAX_DEPTH) {
        OB_RETURN_ERROR(stream, "max nesting depth exceeded");
    }

    uint8_t *base = (uint8_t *)dst_struct;
    uint8_t seen_bits[32];
    bool check_required = false;
    for (uint16_t i = 0; i < desc->field_count; i++) {
        if ((desc->fields[i].flags & OB_FLAG_REQUIRED) != 0u) {
            check_required = true;
            break;
        }
    }
    if (check_required) {
        memset(seen_bits, 0, sizeof(seen_bits));
    }

    while (stream->bytes_left > 0u) {
        uint64_t raw_tag = 0;
        if (!ob_varint_decode(stream, &raw_tag)) {
            return false;
        }
        uint64_t fnum = raw_tag >> 4;
        uint8_t wt = (uint8_t)(raw_tag & 0x0Fu);

        if (fnum > OB_MAX_FIELD_NUM) {
            OB_RETURN_ERROR(stream, "invalid field number");
        }

        /* Silently skip Field #0 (SchemaMeta). */
        if (fnum == 0u) {
            if (wt != OB_WIRE_SUBMSG) {
                OB_RETURN_ERROR(stream, "invalid Field #0 wire type");
            }
            if (!ob_skip_value(stream, OB_WIRE_SUBMSG)) {
                return false;
            }
            continue;
        }

        const ob_field_t *field = NULL;
        uint16_t field_idx = 0;
        if (fnum <= UINT16_MAX) {
            field = find_field(desc, (uint16_t)fnum, &field_idx);
        }

        if (field == NULL) {
            if (!ob_skip_value(stream, wt)) {
                return false;
            }
            continue;
        }

        if (check_required && field_idx < 256u) {
            seen_bits[field_idx / 8u] |= (uint8_t)(1u << (field_idx % 8u));
        }

        if ((field->flags & OB_FLAG_MAP) != 0u) {
            if (wt != OB_WIRE_MAP) {
                if (!ob_skip_value(stream, wt)) {
                    return false;
                }
                continue;
            }
            if (!decode_map_field(stream, field, base, depth)) {
                return false;
            }
            continue;
        }

        if ((field->flags & OB_FLAG_REPEATED) != 0u) {
            if (wt != OB_WIRE_ARRAY) {
                if (!ob_skip_value(stream, wt)) {
                    return false;
                }
                continue;
            }
            if (!decode_array_field(stream, field, base, depth)) {
                return false;
            }
            continue;
        }

        /* Singular / Optional / Required / Oneof */
        if (field->wire_type == OB_WIRE_BOOL_FALSE) {
            if (wt != OB_WIRE_BOOL_FALSE && wt != OB_WIRE_BOOL_TRUE) {
                if (!ob_skip_value(stream, wt)) {
                    return false;
                }
                continue;
            }
            bool bval = (wt == OB_WIRE_BOOL_TRUE);
            if ((field->flags & OB_FLAG_ONEOF) != 0u) {
                uint16_t tag16 = field->tag;
                memcpy(base + field->size_offset, &tag16, sizeof(tag16));
            } else if ((field->flags & OB_FLAG_OPTIONAL) != 0u) {
                bool has_val = true;
                memcpy(base + field->size_offset, &has_val, sizeof(has_val));
            }
            memcpy(base + field->data_offset, &bval, sizeof(bval));
            continue;
        }

        if (wt != field->wire_type) {
            if (!ob_skip_value(stream, wt)) {
                return false;
            }
            continue;
        }

        uint8_t *dst_ptr = base + field->data_offset;
        if ((field->flags & OB_FLAG_ONEOF) != 0u) {
            uint16_t prev_which = 0;
            memcpy(&prev_which, base + field->size_offset, sizeof(prev_which));
            if (prev_which != field->tag) {
                if (field->wire_type == OB_WIRE_SUBMSG) {
                    memset(dst_ptr, 0, field->data_size);
                } else if (field->wire_type == OB_WIRE_BYTES &&
                           (field->flags & OB_FLAG_FIXED_LEN) == 0u) {
                    memset(dst_ptr, 0, OB_BYTES_STRIDE(field->data_size));
                } else {
                    memset(dst_ptr, 0, field->data_size);
                }
            }
            uint16_t tag16 = field->tag;
            memcpy(base + field->size_offset, &tag16, sizeof(tag16));
        } else if ((field->flags & OB_FLAG_OPTIONAL) != 0u) {
            bool has_val = true;
            memcpy(base + field->size_offset, &has_val, sizeof(has_val));
        }

        if (!decode_payload(stream, field, dst_ptr, depth)) {
            return false;
        }
    }

    if (check_required) {
        for (uint16_t i = 0; i < desc->field_count && i < 256u; i++) {
            if ((desc->fields[i].flags & OB_FLAG_REQUIRED) != 0u) {
                if ((seen_bits[i / 8u] & (uint8_t)(1u << (i % 8u))) == 0u) {
                    OB_RETURN_ERROR(stream, "missing required field");
                }
            }
        }
    }

    return true;
}

bool ob_decode_noinit(ob_istream_t *stream, const ob_msgdesc_t *desc, void *dst_struct) {
    if (stream == NULL || desc == NULL || dst_struct == NULL) {
        if (stream != NULL) {
            stream->errmsg = "null argument";
        }
        return false;
    }
    return decode_msg_depth(stream, desc, dst_struct, 0u);
}

bool ob_decode(ob_istream_t *stream, const ob_msgdesc_t *desc, void *dst_struct) {
    if (stream == NULL || desc == NULL || dst_struct == NULL) {
        if (stream != NULL) {
            stream->errmsg = "null argument";
        }
        return false;
    }
    memset(dst_struct, 0, desc->struct_size);
    return decode_msg_depth(stream, desc, dst_struct, 0u);
}
