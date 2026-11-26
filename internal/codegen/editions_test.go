package codegen_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omnibuf/omnibuf-nano/internal/codegen"
	"github.com/omnibuf/omnibuf-nano/internal/options"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

func buildNanoPlugin(t *testing.T) string {
	t.Helper()
	rootDir, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	pluginBin := filepath.Join(t.TempDir(), "protoc-gen-omnibuf-nano")
	cmd := exec.Command("go", "build", "-o", pluginBin, "./cmd/protoc-gen-omnibuf-nano")
	cmd.Dir = rootDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build protoc-gen-omnibuf-nano: %v\n%s", err, string(out))
	}
	return pluginBin
}

func runProtocPlugin(t *testing.T, pluginBin string, protoFiles map[string]string, target string) string {
	t.Helper()
	dir := t.TempDir()
	outDir := filepath.Join(dir, "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, src := range protoFiles {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	protoDir, err := filepath.Abs("../../proto")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("protoc",
		"-I", dir,
		"-I", protoDir,
		"--plugin=protoc-gen-omnibuf-nano="+pluginBin,
		"--omnibuf-nano_out="+outDir,
		"--omnibuf-nano_opt=options_path="+dir,
		filepath.Join(dir, target),
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("protoc plugin failed for %s: %v\n%s", target, err, string(out))
	}
	return outDir
}

func compileAndRunC99(t *testing.T, genDir, genSource, driverName, driverSrc string) {
	t.Helper()
	runtimeDir, err := filepath.Abs("../../runtime")
	if err != nil {
		t.Fatal(err)
	}
	driverPath := filepath.Join(genDir, driverName)
	if err := os.WriteFile(driverPath, []byte(driverSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	// Verify strict C99 syntax check under -fshort-enums.
	shortEnumCmd := exec.Command("cc",
		"-fsyntax-only",
		"-Wall", "-Wextra", "-Werror", "-std=c99", "-pedantic", "-fshort-enums",
		"-I", runtimeDir,
		"-I", genDir,
		filepath.Join(genDir, genSource),
		driverPath,
	)
	if out, err := shortEnumCmd.CombinedOutput(); err != nil {
		t.Fatalf("C99 -fshort-enums compile failed: %v\n%s", err, string(out))
	}

	// Compile and link with ASan + UBSan and strict C99 warning flags.
	binPath := filepath.Join(genDir, "run_test")
	cc := exec.Command("cc",
		"-Wall", "-Wextra", "-Werror", "-std=c99", "-pedantic",
		"-Wshadow", "-Wundef", "-Wcast-align", "-Wstrict-aliasing=2", "-Wdouble-promotion",
		"-fsanitize=address,undefined",
		"-I", runtimeDir,
		"-I", genDir,
		filepath.Join(runtimeDir, "ob_varint.c"),
		filepath.Join(runtimeDir, "ob_encode.c"),
		filepath.Join(runtimeDir, "ob_decode.c"),
		filepath.Join(genDir, genSource),
		driverPath,
		"-o", binPath,
	)
	if out, err := cc.CombinedOutput(); err != nil {
		t.Fatalf("C99 compile failed: %v\n%s", err, string(out))
	}

	run := exec.Command(binPath)
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("C99 test binary failed: %v\n%s", err, string(out))
	}
}

func TestEditions2023_2024_2026_E2E_C99(t *testing.T) {
	pluginBin := buildNanoPlugin(t)

	t.Run("Edition2023_AllFeatures_Runtime", func(t *testing.T) {
		protoSrc := `
edition = "2023";
package ed23;

import "google/protobuf/descriptor.proto";
import "omnibuf/nano/v1/options.proto";

option features.json_format = LEGACY_BEST_EFFORT;

// Custom descriptor option extensions remain allowed in Editions.
extend google.protobuf.FieldOptions {
  string custom_note = 51101;
}

enum OpenState {
  OPEN_STATE_UNSPECIFIED = 0;
  OPEN_STATE_ACTIVE = 1;
}

message Child {
  int32 id = 1 [default = 0];
  string label = 2 [default = "", (omnibuf.nano.v1.field).max_size = 24];
}

message FullEd23 {
  int32 f_i32 = 1 [default = 0];
  int64 f_i64 = 2;
  uint32 f_u32 = 3;
  uint64 f_u64 = 4;
  sint32 f_s32 = 5;
  sint64 f_s64 = 6;
  fixed32 f_fix32 = 7;
  fixed64 f_fix64 = 8;
  sfixed32 f_sfix32 = 9;
  sfixed64 f_sfix64 = 10;
  float f_fl = 11 [default = 0.0];
  double f_db = 12 [default = 0.0];
  bool f_bool = 13 [default = false];
  OpenState f_enum = 14 [default = OPEN_STATE_UNSPECIFIED];
  string f_str = 15 [(omnibuf.nano.v1.field).max_size = 24];
  bytes f_bytes = 16 [(omnibuf.nano.v1.field).max_size = 16];
  bytes f_fix_bytes = 17 [(omnibuf.nano.v1.field) = {max_size: 4, fixed_length: true}];

  // Field-level IMPLICIT presence override
  int32 imp_i32 = 18 [features.field_presence = IMPLICIT];
  bool imp_bool = 19 [features.field_presence = IMPLICIT];
  string imp_str = 20 [features.field_presence = IMPLICIT, (omnibuf.nano.v1.field).max_size = 16];

  // Repeated PACKED and EXPANDED
  repeated uint32 rep_packed = 21 [features.repeated_field_encoding = PACKED, (omnibuf.nano.v1.field).max_count = 4];
  repeated uint32 rep_expanded = 22 [features.repeated_field_encoding = EXPANDED, (omnibuf.nano.v1.field).max_count = 4];

  // Submessage LENGTH_PREFIXED and DELIMITED (singular & repeated)
  Child lp_child = 23 [features.message_encoding = LENGTH_PREFIXED];
  Child delim_child = 24 [features.message_encoding = DELIMITED];
  repeated Child delim_list = 25 [features.message_encoding = DELIMITED, (omnibuf.nano.v1.field).max_count = 4];

  // Maps
  map<string, int32> map_si = 26 [(omnibuf.nano.v1.field) = {max_count: 4, max_size: 16}];
  map<string, Child> map_sc = 27 [(omnibuf.nano.v1.field) = {max_count: 4, max_size: 16}];

  // Oneof including zero/false/empty and DELIMITED message arm
  oneof pick {
    int32 o_i32 = 28;
    bool o_bool = 29;
    string o_str = 30 [(omnibuf.nano.v1.field).max_size = 16];
    bytes o_bytes = 31 [(omnibuf.nano.v1.field).max_size = 8];
    Child o_delim = 32 [features.message_encoding = DELIMITED];
  }
}

// Separate identical messages to prove LENGTH_PREFIXED and DELIMITED emit identical OmniBuf wire bytes.
message WireLP {
  Child c = 1 [features.message_encoding = LENGTH_PREFIXED];
}
message WireDelim {
  Child c = 1 [features.message_encoding = DELIMITED];
}
`
		files := map[string]string{"ed23.proto": protoSrc}

		// 1. Verify direct codegen.Generate succeeds.
		fd := compileProtoDescriptor(t, files, "ed23.proto")
		genDirect, err := codegen.Generate(fd, codegen.Config{})
		if err != nil {
			t.Fatalf("codegen.Generate(ed23) failed: %v", err)
		}
		if !strings.Contains(genDirect[0].Content, "bool has_f_i32;") {
			t.Fatalf("expected explicit presence has_f_i32 in header:\n%s", genDirect[0].Content)
		}
		if strings.Contains(genDirect[0].Content, "bool has_imp_i32;") {
			t.Fatalf("implicit field imp_i32 should not have has_imp_i32:\n%s", genDirect[0].Content)
		}

		// 2. Verify real protoc --plugin=protoc-gen-omnibuf-nano execution.
		outDir := runProtocPlugin(t, pluginBin, files, "ed23.proto")

		driver := `
#include <assert.h>
#include <string.h>
#include "ob_decode.h"
#include "ob_encode.h"
#include "ob_varint.h"
#include "ed23.ob.h"

typedef char _check_enum_size[(sizeof(ed23_OpenState) == 4u) ? 1 : -1];
typedef char _check_enum_signed[(((ed23_OpenState)-1) < 0) ? 1 : -1];

int main(void) {
    /* 1. Absent vs explicit zero across every scalar/string/bytes/enum/submsg */
    {
        ed23_FullEd23 empty = ed23_FullEd23_init_zero;
        uint8_t wire[512];
        ob_ostream_t os = ob_ostream_from_buffer(wire, sizeof(wire));
        assert(ob_encode(&os, &ed23_FullEd23_msg, &empty));
        assert(os.bytes_written == 0u);

        ed23_FullEd23 dec;
        memset(&dec, 0xAA, sizeof(dec));
        ob_istream_t is = ob_istream_from_buffer(wire, os.bytes_written);
        assert(ob_decode(&is, &ed23_FullEd23_msg, &dec));
        assert(!dec.has_f_i32 && !dec.has_f_bool && !dec.has_f_str && !dec.has_f_bytes);
        assert(!dec.has_f_fix_bytes && !dec.has_f_enum && !dec.has_lp_child && !dec.has_delim_child);
        assert(dec.which_pick == 0u);
    }

    {
        ed23_FullEd23 zeros = ed23_FullEd23_init_zero;
        zeros.has_f_i32 = true;
        zeros.has_f_i64 = true;
        zeros.has_f_u32 = true;
        zeros.has_f_u64 = true;
        zeros.has_f_s32 = true;
        zeros.has_f_s64 = true;
        zeros.has_f_fix32 = true;
        zeros.has_f_fix64 = true;
        zeros.has_f_sfix32 = true;
        zeros.has_f_sfix64 = true;
        zeros.has_f_fl = true;
        zeros.has_f_db = true;
        zeros.has_f_bool = true;
        zeros.f_bool = false;
        zeros.has_f_enum = true;
        zeros.f_enum = ed23_OpenState_OPEN_STATE_UNSPECIFIED;
        zeros.has_f_str = true;
        zeros.has_f_bytes = true;
        zeros.has_f_fix_bytes = true;
        zeros.has_lp_child = true;
        zeros.has_delim_child = true;
        zeros.which_pick = ed23_FullEd23_o_bool_tag;
        zeros.pick.o_bool = false;

        uint8_t wire[512];
        ob_ostream_t os = ob_ostream_from_buffer(wire, sizeof(wire));
        assert(ob_encode(&os, &ed23_FullEd23_msg, &zeros));
        assert(os.bytes_written > 0u);

        ed23_FullEd23 dec;
        memset(&dec, 0xAA, sizeof(dec));
        ob_istream_t is = ob_istream_from_buffer(wire, os.bytes_written);
        assert(ob_decode(&is, &ed23_FullEd23_msg, &dec));
        assert(is.bytes_left == 0u);

        assert(dec.has_f_i32 && dec.f_i32 == 0);
        assert(dec.has_f_i64 && dec.f_i64 == 0);
        assert(dec.has_f_u32 && dec.f_u32 == 0u);
        assert(dec.has_f_u64 && dec.f_u64 == 0u);
        assert(dec.has_f_s32 && dec.f_s32 == 0);
        assert(dec.has_f_s64 && dec.f_s64 == 0);
        assert(dec.has_f_fix32 && dec.f_fix32 == 0u);
        assert(dec.has_f_fix64 && dec.f_fix64 == 0u);
        assert(dec.has_f_sfix32 && dec.f_sfix32 == 0);
        assert(dec.has_f_sfix64 && dec.f_sfix64 == 0);
        assert(dec.has_f_fl && dec.f_fl == 0.0f);
        assert(dec.has_f_db && dec.f_db == 0.0);
        assert(dec.has_f_bool && dec.f_bool == false);
        assert(dec.has_f_enum && dec.f_enum == 0);
        assert(dec.has_f_str && dec.f_str[0] == '\0');
        assert(dec.has_f_bytes && dec.f_bytes.size == 0u);
        assert(dec.has_f_fix_bytes && dec.f_fix_bytes[0] == 0u && dec.f_fix_bytes[3] == 0u);
        assert(dec.has_lp_child && !dec.lp_child.has_id && !dec.lp_child.has_label);
        assert(dec.has_delim_child && !dec.delim_child.has_id && !dec.delim_child.has_label);
        assert(dec.which_pick == ed23_FullEd23_o_bool_tag && dec.pick.o_bool == false);
        /* Implicit fields were zero and must remain zero. */
        assert(dec.imp_i32 == 0 && dec.imp_bool == false && dec.imp_str[0] == '\0');
    }

    /* 2. DELIMITED emits exact same OmniBuf wire bytes as LENGTH_PREFIXED */
    {
        ed23_WireLP lp = ed23_WireLP_init_zero;
        lp.has_c = true;
        lp.c.has_id = true;
        lp.c.id = -17;
        lp.c.has_label = true;
        strcpy(lp.c.label, "same-wire");

        ed23_WireDelim dl = ed23_WireDelim_init_zero;
        dl.has_c = true;
        dl.c.has_id = true;
        dl.c.id = -17;
        dl.c.has_label = true;
        strcpy(dl.c.label, "same-wire");

        uint8_t buf_lp[64];
        uint8_t buf_dl[64];
        ob_ostream_t os_lp = ob_ostream_from_buffer(buf_lp, sizeof(buf_lp));
        ob_ostream_t os_dl = ob_ostream_from_buffer(buf_dl, sizeof(buf_dl));
        assert(ob_encode(&os_lp, &ed23_WireLP_msg, &lp));
        assert(ob_encode(&os_dl, &ed23_WireDelim_msg, &dl));
        assert(os_lp.bytes_written == os_dl.bytes_written);
        assert(memcmp(buf_lp, buf_dl, os_lp.bytes_written) == 0);
    }

    /* 3. Rich values: unknown open enum, implicit non-zero, PACKED & EXPANDED, DELIMITED list, maps, DELIMITED oneof */
    {
        ed23_FullEd23 src = ed23_FullEd23_init_zero;
        src.has_f_enum = true;
        src.f_enum = 98765; /* Unknown open enum value */
        src.imp_i32 = -55;
        src.imp_bool = true;
        strcpy(src.imp_str, "implicit-ok");

        src.rep_packed_count = 3u;
        src.rep_packed[0] = 1u;
        src.rep_packed[1] = 200u;
        src.rep_packed[2] = 30000u;

        src.rep_expanded_count = 2u;
        src.rep_expanded[0] = 7u;
        src.rep_expanded[1] = 8u;

        src.delim_list_count = 2u;
        src.delim_list[0].has_id = true;
        src.delim_list[0].id = 11;
        src.delim_list[0].has_label = true;
        strcpy(src.delim_list[0].label, "d0");
        src.delim_list[1].has_id = true;
        src.delim_list[1].id = 0; /* explicit zero inside repeated DELIMITED child */

        src.map_si_count = 2u;
        strcpy(src.map_si[0].key, "k1");
        src.map_si[0].value = -10;
        strcpy(src.map_si[1].key, "k2");
        src.map_si[1].value = 0;

        src.map_sc_count = 1u;
        strcpy(src.map_sc[0].key, "mc");
        src.map_sc[0].value.has_id = true;
        src.map_sc[0].value.id = 42;
        src.map_sc[0].value.has_label = true;
        strcpy(src.map_sc[0].value.label, "mapped-child");

        src.which_pick = ed23_FullEd23_o_delim_tag;
        src.pick.o_delim.has_id = true;
        src.pick.o_delim.id = 99;
        src.pick.o_delim.has_label = true;
        strcpy(src.pick.o_delim.label, "oneof-delim");

        uint8_t wire[1024];
        ob_ostream_t os = ob_ostream_from_buffer(wire, sizeof(wire));
        assert(ob_encode(&os, &ed23_FullEd23_msg, &src));

        ed23_FullEd23 dec;
        memset(&dec, 0xAA, sizeof(dec));
        ob_istream_t is = ob_istream_from_buffer(wire, os.bytes_written);
        assert(ob_decode(&is, &ed23_FullEd23_msg, &dec));
        assert(is.bytes_left == 0u);

        assert(dec.has_f_enum && dec.f_enum == 98765);
        assert(dec.imp_i32 == -55 && dec.imp_bool == true && strcmp(dec.imp_str, "implicit-ok") == 0);
        assert(dec.rep_packed_count == 3u && dec.rep_packed[0] == 1u && dec.rep_packed[2] == 30000u);
        assert(dec.rep_expanded_count == 2u && dec.rep_expanded[0] == 7u && dec.rep_expanded[1] == 8u);
        assert(dec.delim_list_count == 2u);
        assert(dec.delim_list[0].has_id && dec.delim_list[0].id == 11 && strcmp(dec.delim_list[0].label, "d0") == 0);
        assert(dec.delim_list[1].has_id && dec.delim_list[1].id == 0 && !dec.delim_list[1].has_label);
        assert(dec.map_si_count == 2u && strcmp(dec.map_si[0].key, "k1") == 0 && dec.map_si[0].value == -10);
        assert(strcmp(dec.map_si[1].key, "k2") == 0 && dec.map_si[1].value == 0);
        assert(dec.map_sc_count == 1u && strcmp(dec.map_sc[0].key, "mc") == 0);
        assert(dec.map_sc[0].value.has_id && dec.map_sc[0].value.id == 42);
        assert(dec.map_sc[0].value.has_label && strcmp(dec.map_sc[0].value.label, "mapped-child") == 0);
        assert(dec.which_pick == ed23_FullEd23_o_delim_tag);
        assert(dec.pick.o_delim.has_id && dec.pick.o_delim.id == 99);
        assert(dec.pick.o_delim.has_label && strcmp(dec.pick.o_delim.label, "oneof-delim") == 0);
    }

    /* 4. Oneof switching on wire clears previous union arm */
    {
        uint8_t wire[128];
        ob_ostream_t os = ob_ostream_from_buffer(wire, sizeof(wire));
        assert(ob_write_tag(&os, ed23_FullEd23_o_str_tag, OB_WIRE_STRING));
        assert(ob_varint_encode(&os, 4u));
        assert(ob_write(&os, (const uint8_t *)"prev", 4u));
        assert(ob_write_tag(&os, ed23_FullEd23_o_bytes_tag, OB_WIRE_BYTES));
        assert(ob_varint_encode(&os, 0u));

        ed23_FullEd23 dec;
        ob_istream_t is = ob_istream_from_buffer(wire, os.bytes_written);
        assert(ob_decode(&is, &ed23_FullEd23_msg, &dec));
        assert(dec.which_pick == ed23_FullEd23_o_bytes_tag);
        assert(dec.pick.o_bytes.size == 0u);
    }

    return 0;
}
`
		compileAndRunC99(t, outDir, "ed23.ob.c", "test_ed23.c", driver)
	})

	t.Run("Edition2024_InheritedFeaturesAndVisibility_Runtime", func(t *testing.T) {
		protoSrc := `
edition = "2024";
package ed24;

import option "omnibuf/nano/v1/options.proto";

option features.field_presence = IMPLICIT;
option features.default_symbol_visibility = EXPORT_TOP_LEVEL;

enum Status {
  STATUS_UNSPECIFIED = 0;
  STATUS_READY = 1;
}

message Inner {
  int32 code = 1 [features.field_presence = EXPLICIT];
  string note = 2; // sidecar max_size
}

message Record {
  // Inherits file-level IMPLICIT presence
  int32 imp_count = 1;
  bool imp_flag = 2;
  Status imp_status = 3;

  // Field-level EXPLICIT override
  int32 exp_count = 4 [features.field_presence = EXPLICIT];
  bool exp_flag = 5 [features.field_presence = EXPLICIT];
  string exp_str = 6 [features.field_presence = EXPLICIT, (omnibuf.nano.v1.field).max_size = 20];

  // Submessage always has explicit presence, including DELIMITED
  Inner delim_inner = 7 [features.message_encoding = DELIMITED];
  repeated Inner delim_items = 8 [features.message_encoding = DELIMITED, (omnibuf.nano.v1.field).max_count = 3];

  // Oneof always has explicit presence even when file default is IMPLICIT
  oneof choice {
    int32 c_zero = 9;
    bool c_false = 10;
    string c_empty = 11 [(omnibuf.nano.v1.field).max_size = 16];
  }
}
`
		sidecar := "Inner.note max_size:24\n"
		files := map[string]string{
			"ed24.proto":   protoSrc,
			"ed24.options": sidecar,
		}

		outDir := runProtocPlugin(t, pluginBin, files, "ed24.proto")
		hdrBytes, err := os.ReadFile(filepath.Join(outDir, "ed24.ob.h"))
		if err != nil {
			t.Fatal(err)
		}
		hdr := string(hdrBytes)
		if strings.Contains(hdr, "has_imp_count") || strings.Contains(hdr, "has_imp_flag") {
			t.Fatalf("inherited IMPLICIT fields should not have has_*:\n%s", hdr)
		}
		if !strings.Contains(hdr, "bool has_exp_count;") || !strings.Contains(hdr, "bool has_exp_flag;") {
			t.Fatalf("EXPLICIT override fields missing has_*:\n%s", hdr)
		}
		if !strings.Contains(hdr, "char note[24u];") {
			t.Fatalf("sidecar max_size did not apply in Edition 2024:\n%s", hdr)
		}

		driver := `
#include <assert.h>
#include <string.h>
#include "ob_decode.h"
#include "ob_encode.h"
#include "ed24.ob.h"

int main(void) {
    /* Zero implicit fields emit 0 bytes; explicit zero fields emit bytes and round-trip. */
    ed24_Record src = ed24_Record_init_zero;
    src.imp_count = 0;
    src.imp_flag = false;
    src.imp_status = 0;
    src.has_exp_count = true;
    src.exp_count = 0;
    src.has_exp_flag = true;
    src.exp_flag = false;
    src.has_exp_str = true;
    src.exp_str[0] = '\0';
    src.has_delim_inner = true;
    src.delim_inner.has_code = true;
    src.delim_inner.code = 0;
    src.delim_items_count = 1u;
    src.delim_items[0].has_code = true;
    src.delim_items[0].code = -7;
    strcpy(src.delim_items[0].note, "ed24-item");
    src.which_choice = ed24_Record_c_empty_tag;
    src.choice.c_empty[0] = '\0';

    uint8_t wire[256];
    ob_ostream_t os = ob_ostream_from_buffer(wire, sizeof(wire));
    assert(ob_encode(&os, &ed24_Record_msg, &src));
    assert(os.bytes_written > 0u);

    ed24_Record dec;
    memset(&dec, 0xAA, sizeof(dec));
    ob_istream_t is = ob_istream_from_buffer(wire, os.bytes_written);
    assert(ob_decode(&is, &ed24_Record_msg, &dec));
    assert(is.bytes_left == 0u);

    assert(dec.imp_count == 0 && dec.imp_flag == false && dec.imp_status == 0);
    assert(dec.has_exp_count && dec.exp_count == 0);
    assert(dec.has_exp_flag && dec.exp_flag == false);
    assert(dec.has_exp_str && dec.exp_str[0] == '\0');
    assert(dec.has_delim_inner && dec.delim_inner.has_code && dec.delim_inner.code == 0);
    assert(dec.delim_items_count == 1u && dec.delim_items[0].has_code && dec.delim_items[0].code == -7);
    assert(strcmp(dec.delim_items[0].note, "ed24-item") == 0);
    assert(dec.which_choice == ed24_Record_c_empty_tag && dec.choice.c_empty[0] == '\0');
    return 0;
}
`
		compileAndRunC99(t, outDir, "ed24.ob.c", "test_ed24.c", driver)
	})

	t.Run("Edition2026_StrictVisibilityAndExport_Runtime", func(t *testing.T) {
		protoSrc := `
edition = "2026";
package ed26;

import option "omnibuf/nano/v1/options.proto";

option features.default_symbol_visibility = STRICT;

export enum Kind {
  KIND_UNSPECIFIED = 0;
  KIND_ALPHA = 1;
}

local message LocalDetail {
  int32 seq = 1;
  string info = 2 [(omnibuf.nano.v1.field).max_size = 24];
}

export message Envelope {
  Kind kind = 1;
  bool flag = 2;
  int32 imp_val = 3 [features.field_presence = IMPLICIT];
  string title = 4 [(omnibuf.nano.v1.field).max_size = 32];
  bytes payload = 5 [(omnibuf.nano.v1.field).max_size = 16];
  LocalDetail detail = 6 [features.message_encoding = DELIMITED];
  repeated LocalDetail extras = 7 [features.message_encoding = DELIMITED, (omnibuf.nano.v1.field).max_count = 3];
  repeated int32 expanded_nums = 8 [features.repeated_field_encoding = EXPANDED, (omnibuf.nano.v1.field).max_count = 4];
  map<string, int32> attrs = 9 [(omnibuf.nano.v1.field) = {max_count: 4, max_size: 16}];
  oneof body {
    int32 body_num = 10;
    bool body_bool = 11;
    string body_text = 12 [(omnibuf.nano.v1.field).max_size = 24];
  }
}
`
		files := map[string]string{"ed26.proto": protoSrc}

		// Verify direct codegen.Generate with Edition 2026 descriptor.
		fd := compileProtoDescriptor(t, files, "ed26.proto")
		if _, err := codegen.Generate(fd, codegen.Config{}); err != nil {
			t.Fatalf("codegen.Generate(ed26) failed: %v", err)
		}

		// Verify real protoc 36.2 + protoc-gen-omnibuf-nano binary on Edition 2026!
		outDir := runProtocPlugin(t, pluginBin, files, "ed26.proto")

		driver := `
#include <assert.h>
#include <string.h>
#include "ob_decode.h"
#include "ob_encode.h"
#include "ed26.ob.h"

typedef char _check_enum26_size[(sizeof(ed26_Kind) == 4u) ? 1 : -1];
typedef char _check_enum26_signed[(((ed26_Kind)-1) < 0) ? 1 : -1];

int main(void) {
    ed26_Envelope src = ed26_Envelope_init_zero;
    src.has_kind = true;
    src.kind = -321; /* unknown negative enum value */
    src.has_flag = true;
    src.flag = false;
    src.imp_val = 4096;
    src.has_title = true;
    src.title[0] = '\0';
    src.has_payload = true;
    src.payload.size = 0u;
    src.has_detail = true;
    src.detail.has_seq = true;
    src.detail.seq = 0;
    src.detail.has_info = true;
    strcpy(src.detail.info, "ed26-local");
    src.extras_count = 2u;
    src.extras[0].has_seq = true;
    src.extras[0].seq = 1;
    src.extras[1].has_seq = true;
    src.extras[1].seq = 2;
    src.expanded_nums_count = 2u;
    src.expanded_nums[0] = -100;
    src.expanded_nums[1] = 200;
    src.attrs_count = 1u;
    strcpy(src.attrs[0].key, "edition");
    src.attrs[0].value = 2026;
    src.which_body = ed26_Envelope_body_num_tag;
    src.body.body_num = 0;

    uint8_t wire[512];
    ob_ostream_t os = ob_ostream_from_buffer(wire, sizeof(wire));
    assert(ob_encode(&os, &ed26_Envelope_msg, &src));
    assert(os.bytes_written > 0u);

    ed26_Envelope dec;
    memset(&dec, 0xAA, sizeof(dec));
    ob_istream_t is = ob_istream_from_buffer(wire, os.bytes_written);
    assert(ob_decode(&is, &ed26_Envelope_msg, &dec));
    assert(is.bytes_left == 0u);

    assert(dec.has_kind && dec.kind == -321);
    assert(dec.has_flag && dec.flag == false);
    assert(dec.imp_val == 4096);
    assert(dec.has_title && dec.title[0] == '\0');
    assert(dec.has_payload && dec.payload.size == 0u);
    assert(dec.has_detail && dec.detail.has_seq && dec.detail.seq == 0);
    assert(dec.detail.has_info && strcmp(dec.detail.info, "ed26-local") == 0);
    assert(dec.extras_count == 2u && dec.extras[0].seq == 1 && dec.extras[1].seq == 2);
    assert(dec.expanded_nums_count == 2u && dec.expanded_nums[0] == -100 && dec.expanded_nums[1] == 200);
    assert(dec.attrs_count == 1u && strcmp(dec.attrs[0].key, "edition") == 0 && dec.attrs[0].value == 2026);
    assert(dec.which_body == ed26_Envelope_body_num_tag && dec.body.body_num == 0);
    return 0;
}
`
		compileAndRunC99(t, outDir, "ed26.ob.c", "test_ed26.c", driver)
	})
}

type fakeEditionFile struct {
	protoreflect.FileDescriptor
	ed int32
}

func (f fakeEditionFile) Edition() int32 { return f.ed }

func TestEditionsUnsupportedVersionsRejected(t *testing.T) {
	baseFD := compileProtoDescriptor(t, map[string]string{
		"base.proto": `edition = "2023"; message M { int32 a = 1; }`,
	}, "base.proto")

	for _, ed := range []int32{0, 1003, int32(descriptorpb.Edition_EDITION_UNSTABLE)} {
		t.Run(fmt.Sprintf("edition_%d", ed), func(t *testing.T) {
			_, err := codegen.Generate(fakeEditionFile{FileDescriptor: baseFD, ed: ed}, codegen.Config{})
			if err == nil {
				t.Fatalf("expected error for unsupported edition %d, got nil", ed)
			}
			if !strings.Contains(err.Error(), "unsupported protobuf edition") {
				t.Fatalf("expected 'unsupported protobuf edition' in error, got: %v", err)
			}
		})
	}

	// Also verify real protodesc.NewFiles with EDITION_UNSTABLE (9999) is rejected.
	t.Run("real_protodesc_EDITION_UNSTABLE", func(t *testing.T) {
		fds := &descriptorpb.FileDescriptorSet{
			File: []*descriptorpb.FileDescriptorProto{
				{
					Name:    proto.String("unstable.proto"),
					Syntax:  proto.String("editions"),
					Edition: descriptorpb.Edition_EDITION_UNSTABLE.Enum(),
					MessageType: []*descriptorpb.DescriptorProto{
						{
							Name: proto.String("M"),
							Field: []*descriptorpb.FieldDescriptorProto{
								{
									Name:   proto.String("x"),
									Number: proto.Int32(1),
									Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
									Type:   descriptorpb.FieldDescriptorProto_TYPE_INT32.Enum(),
								},
							},
						},
					},
				},
			},
		}
		files, err := protodesc.NewFiles(fds)
		if err != nil {
			t.Fatal(err)
		}
		fd, err := files.FindFileByPath("unstable.proto")
		if err != nil {
			t.Fatal(err)
		}
		_, err = codegen.Generate(fd, codegen.Config{})
		if err == nil || !strings.Contains(err.Error(), "unsupported protobuf edition") {
			t.Fatalf("expected unsupported protobuf edition error for EDITION_UNSTABLE, got: %v", err)
		}
	})
}

func TestEditionsValidationErrors(t *testing.T) {
	cases := []struct {
		name      string
		files     map[string]string
		target    string
		wantSub   string
		sidecar   string
		expectErr bool
	}{
		{
			name: "closed_enum_file_level",
			files: map[string]string{
				"c.proto": `edition = "2023"; option features.enum_type = CLOSED; enum E { E_ZERO = 0; } message M { E e = 1; }`,
			},
			target:    "c.proto",
			wantSub:   "CLOSED",
			expectErr: true,
		},
		{
			name: "closed_enum_level_unused",
			files: map[string]string{
				"c.proto": `edition = "2023"; enum E { option features.enum_type = CLOSED; E_ZERO = 0; } message M { int32 x = 1; }`,
			},
			target:    "c.proto",
			wantSub:   "CLOSED",
			expectErr: true,
		},
		{
			name: "closed_enum_nested",
			files: map[string]string{
				"c.proto": `edition = "2024"; message M { enum Nested { option features.enum_type = CLOSED; N_ZERO = 0; } Nested n = 1; }`,
			},
			target:    "c.proto",
			wantSub:   "CLOSED",
			expectErr: true,
		},
		{
			name: "closed_enum_cross_file_proto2",
			files: map[string]string{
				"p2.proto": `syntax = "proto2"; package p2; enum LegacyClosed { ZERO = 0; ONE = 1; }`,
				"c.proto":  `edition = "2024"; import "p2.proto"; message M { p2.LegacyClosed e = 1; }`,
			},
			target:    "c.proto",
			wantSub:   "CLOSED",
			expectErr: true,
		},
		{
			name: "closed_enum_cross_file_map_value",
			files: map[string]string{
				"p2.proto": `syntax = "proto2"; package p2; enum LegacyClosed { ZERO = 0; ONE = 1; }`,
				"c.proto":  `edition = "2024"; import "p2.proto"; message M { map<int32, p2.LegacyClosed> m = 1; }`,
			},
			target:    "c.proto",
			sidecar:   "M.m max_count:4\n",
			wantSub:   "CLOSED",
			expectErr: true,
		},
		{
			name: "legacy_required_rejected",
			files: map[string]string{
				"c.proto": `edition = "2023"; message M { int32 req = 1 [features.field_presence = LEGACY_REQUIRED]; }`,
			},
			target:    "c.proto",
			wantSub:   "LEGACY_REQUIRED",
			expectErr: true,
		},
		{
			name: "utf8_none_field_string",
			files: map[string]string{
				"c.proto": `edition = "2023"; message M { string s = 1 [features.utf8_validation = NONE]; }`,
			},
			target:    "c.proto",
			sidecar:   "M.s max_size:16\n",
			wantSub:   "utf8_validation = NONE",
			expectErr: true,
		},
		{
			name: "utf8_none_repeated_string",
			files: map[string]string{
				"c.proto": `edition = "2023"; message M { repeated string rs = 1 [features.utf8_validation = NONE]; }`,
			},
			target:    "c.proto",
			sidecar:   "M.rs max_count:4 max_size:16\n",
			wantSub:   "utf8_validation = NONE",
			expectErr: true,
		},
		{
			name: "utf8_none_file_inherited_string",
			files: map[string]string{
				"c.proto": `edition = "2024"; option features.utf8_validation = NONE; message M { string s = 1; }`,
			},
			target:    "c.proto",
			sidecar:   "M.s max_size:16\n",
			wantSub:   "utf8_validation = NONE",
			expectErr: true,
		},
		{
			name: "utf8_none_file_inherited_map_key",
			files: map[string]string{
				"c.proto": `edition = "2024"; option features.utf8_validation = NONE; message M { map<string, int32> m = 1; }`,
			},
			target:    "c.proto",
			sidecar:   "M.m max_count:4 max_size:16\n",
			wantSub:   "utf8_validation = NONE",
			expectErr: true,
		},
		{
			name: "utf8_none_file_inherited_map_value",
			files: map[string]string{
				"c.proto": `edition = "2024"; option features.utf8_validation = NONE; message M { map<int32, string> m = 1; }`,
			},
			target:    "c.proto",
			sidecar:   "M.m max_count:4 max_size:16\n",
			wantSub:   "utf8_validation = NONE",
			expectErr: true,
		},
		{
			name: "utf8_none_map_field_key",
			files: map[string]string{
				"c.proto": `edition = "2023"; message M { map<string, int32> m = 1 [features.utf8_validation = NONE]; }`,
			},
			target:    "c.proto",
			sidecar:   "M.m max_count:4 max_size:16\n",
			wantSub:   "utf8_validation = NONE",
			expectErr: true,
		},
		{
			name: "utf8_none_map_field_value",
			files: map[string]string{
				"c.proto": `edition = "2023"; message M { map<int32, string> m = 1 [features.utf8_validation = NONE]; }`,
			},
			target:    "c.proto",
			sidecar:   "M.m max_count:4 max_size:16\n",
			wantSub:   "utf8_validation = NONE",
			expectErr: true,
		},
		{
			name: "utf8_none_file_overridden_back_to_verify_allowed",
			files: map[string]string{
				"c.proto": `edition = "2024"; option features.utf8_validation = NONE; message M { int32 n = 1; string s = 2 [features.utf8_validation = VERIFY]; }`,
			},
			target:    "c.proto",
			sidecar:   "M.s max_size:16\n",
			expectErr: false,
		},
		{
			name: "nonzero_default_int32",
			files: map[string]string{
				"c.proto": `edition = "2023"; message M { int32 x = 1 [default = 42]; }`,
			},
			target:    "c.proto",
			wantSub:   "non-zero default",
			expectErr: true,
		},
		{
			name: "nonzero_default_bool",
			files: map[string]string{
				"c.proto": `edition = "2023"; message M { bool b = 1 [default = true]; }`,
			},
			target:    "c.proto",
			wantSub:   "non-zero default",
			expectErr: true,
		},
		{
			name: "nonzero_default_float",
			files: map[string]string{
				"c.proto": `edition = "2023"; message M { float f = 1 [default = 1.5]; }`,
			},
			target:    "c.proto",
			wantSub:   "non-zero default",
			expectErr: true,
		},
		{
			name: "nonzero_default_neg_zero_float",
			files: map[string]string{
				"c.proto": `edition = "2023"; message M { float f = 1 [default = -0.0]; }`,
			},
			target:    "c.proto",
			wantSub:   "non-zero default",
			expectErr: true,
		},
		{
			name: "nonzero_default_string",
			files: map[string]string{
				"c.proto": `edition = "2023"; message M { string s = 1 [default = "hi"]; }`,
			},
			target:    "c.proto",
			sidecar:   "M.s max_size:16\n",
			wantSub:   "non-zero default",
			expectErr: true,
		},
		{
			name: "nonzero_default_bytes",
			files: map[string]string{
				"c.proto": `edition = "2023"; message M { bytes b = 1 [default = "ab"]; }`,
			},
			target:    "c.proto",
			sidecar:   "M.b max_size:16\n",
			wantSub:   "non-zero default",
			expectErr: true,
		},
		{
			name: "nonzero_default_enum",
			files: map[string]string{
				"c.proto": `edition = "2023"; enum E { E_ZERO = 0; E_ONE = 1; } message M { E e = 1 [default = E_ONE]; }`,
			},
			target:    "c.proto",
			wantSub:   "non-zero default",
			expectErr: true,
		},
		{
			name: "extension_range_rejected",
			files: map[string]string{
				"c.proto": `edition = "2023"; message M { int32 a = 1; extensions 100 to 199; }`,
			},
			target:    "c.proto",
			wantSub:   "extension ranges",
			expectErr: true,
		},
		{
			name: "top_level_non_option_extension_rejected",
			files: map[string]string{
				"base.proto": `syntax = "proto2"; package base; message Target { extensions 100 to 199; }`,
				"c.proto":    `edition = "2023"; import "base.proto"; extend base.Target { int32 ext_x = 100; } message M { int32 a = 1; }`,
			},
			target:    "c.proto",
			wantSub:   "extensions are not supported",
			expectErr: true,
		},
		{
			name: "nested_non_option_extension_rejected",
			files: map[string]string{
				"base.proto": `syntax = "proto2"; package base; message Target { extensions 100 to 199; }`,
				"c.proto":    `edition = "2023"; import "base.proto"; message M { extend base.Target { int32 ext_x = 100; } int32 a = 1; }`,
			},
			target:    "c.proto",
			wantSub:   "extensions are not supported",
			expectErr: true,
		},
		{
			name: "legacy_proto2_group_still_rejected",
			files: map[string]string{
				"g.proto": `syntax = "proto2"; message M { optional group Legacy = 1 { optional int32 x = 2; } }`,
			},
			target:    "g.proto",
			wantSub:   "unsupported protobuf field kind",
			expectErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fd := compileProtoDescriptor(t, tc.files, tc.target)
			var sidecar *options.File
			if tc.sidecar != "" {
				var err error
				sidecar, err = options.Parse(strings.NewReader(tc.sidecar))
				if err != nil {
					t.Fatal(err)
				}
			}
			_, err := codegen.Generate(fd, codegen.Config{Sidecar: sidecar})
			if tc.expectErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantSub)
				}
				if !strings.Contains(err.Error(), tc.wantSub) {
					t.Fatalf("expected error containing %q, got: %v", tc.wantSub, err)
				}
			} else if err != nil {
				t.Fatalf("expected success, got error: %v", err)
			}
		})
	}
}

func TestEditionsUTF8InheritanceDescriptorLevels(t *testing.T) {
	// Directly verify message-level and oneof-level utf8_validation = NONE inheritance
	// via FileDescriptorProto construction.
	makeFDS := func(msgNone, oneofNone, fieldVerify bool) *descriptorpb.FileDescriptorSet {
		none := descriptorpb.FeatureSet_NONE
		verify := descriptorpb.FeatureSet_VERIFY
		var msgOpts *descriptorpb.MessageOptions
		if msgNone {
			msgOpts = &descriptorpb.MessageOptions{
				Features: &descriptorpb.FeatureSet{Utf8Validation: &none},
			}
		}
		var oneofOpts *descriptorpb.OneofOptions
		if oneofNone {
			oneofOpts = &descriptorpb.OneofOptions{
				Features: &descriptorpb.FeatureSet{Utf8Validation: &none},
			}
		}
		var fieldOpts *descriptorpb.FieldOptions
		if fieldVerify {
			fieldOpts = &descriptorpb.FieldOptions{
				Features: &descriptorpb.FeatureSet{Utf8Validation: &verify},
			}
		}
		return &descriptorpb.FileDescriptorSet{
			File: []*descriptorpb.FileDescriptorProto{
				{
					Name:    proto.String("inh.proto"),
					Syntax:  proto.String("editions"),
					Edition: descriptorpb.Edition_EDITION_2024.Enum(),
					MessageType: []*descriptorpb.DescriptorProto{
						{
							Name:    proto.String("M"),
							Options: msgOpts,
							OneofDecl: []*descriptorpb.OneofDescriptorProto{
								{
									Name:    proto.String("u"),
									Options: oneofOpts,
								},
							},
							Field: []*descriptorpb.FieldDescriptorProto{
								{
									Name:       proto.String("s"),
									Number:     proto.Int32(1),
									Label:      descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
									Type:       descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
									OneofIndex: proto.Int32(0),
									Options:    fieldOpts,
								},
							},
						},
					},
				},
			},
		}
	}

	sidecar, err := options.Parse(strings.NewReader("M.s max_size:16\n"))
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name        string
		msgNone     bool
		oneofNone   bool
		fieldVerify bool
		wantErr     bool
	}{
		{"message_level_none", true, false, false, true},
		{"oneof_level_none", false, true, false, true},
		{"oneof_none_overridden_by_field_verify", false, true, true, false},
		{"message_none_overridden_by_field_verify", true, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files, err := protodesc.NewFiles(makeFDS(tc.msgNone, tc.oneofNone, tc.fieldVerify))
			if err != nil {
				t.Fatal(err)
			}
			fd, err := files.FindFileByPath("inh.proto")
			if err != nil {
				t.Fatal(err)
			}
			_, err = codegen.Generate(fd, codegen.Config{Sidecar: sidecar})
			if tc.wantErr && (err == nil || !strings.Contains(err.Error(), "utf8_validation = NONE")) {
				t.Fatalf("expected utf8_validation = NONE error, got: %v", err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected success, got: %v", err)
			}
		})
	}
}
