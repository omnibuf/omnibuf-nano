package conformance_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/omnibuf/omnibuf-go/omnibufproto"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

func TestCrossLanguageEditionsConformance(t *testing.T) {
	rootDir, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	protoDir := filepath.Join(rootDir, "proto")
	runtimeDir := filepath.Join(rootDir, "runtime")

	pluginDir := t.TempDir()
	pluginBin := filepath.Join(pluginDir, "protoc-gen-omnibuf-nano")
	buildPlugin := exec.Command("go", "build", "-o", pluginBin, "./cmd/protoc-gen-omnibuf-nano")
	buildPlugin.Dir = rootDir
	if out, err := buildPlugin.CombinedOutput(); err != nil {
		t.Fatalf("build plugin: %v\n%s", err, string(out))
	}

	editions := []struct {
		name         string
		editionDecl  string
		importLine   string
		extraOptions string
		exportKw     string
	}{
		{
			name:        "Edition2023",
			editionDecl: `edition = "2023";`,
			importLine:  `import "omnibuf/nano/v1/options.proto";`,
		},
		{
			name:        "Edition2024",
			editionDecl: `edition = "2024";`,
			importLine:  `import option "omnibuf/nano/v1/options.proto";`,
		},
		{
			name:         "Edition2026",
			editionDecl:  `edition = "2026";`,
			importLine:   `import option "omnibuf/nano/v1/options.proto";`,
			extraOptions: "option features.default_symbol_visibility = STRICT;\n",
			exportKw:     "export ",
		},
	}

	for _, ed := range editions {
		t.Run(ed.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			schemaSrc := fmt.Sprintf(`%s
package omnibuf.edtest;
%s
%s
%senum State {
  STATE_UNSPECIFIED = 0;
  STATE_ACTIVE = 1;
}

%smessage Child {
  int32 id = 1;
  string tag = 2 [(omnibuf.nano.v1.field).max_size = 24];
}

%smessage EditionsMsg {
  int32 exp_i32 = 1;
  uint64 exp_u64 = 2;
  bool exp_bool = 3;
  float exp_f32 = 4;
  double exp_f64 = 5;
  State exp_enum = 6;
  string exp_str = 7 [(omnibuf.nano.v1.field).max_size = 32];
  bytes exp_bytes = 8 [(omnibuf.nano.v1.field).max_size = 16];

  int32 imp_i32 = 9 [features.field_presence = IMPLICIT];
  bool imp_bool = 10 [features.field_presence = IMPLICIT];
  string imp_str = 11 [features.field_presence = IMPLICIT, (omnibuf.nano.v1.field).max_size = 24];

  Child child = 12;
  repeated uint32 rep_packed = 13 [features.repeated_field_encoding = PACKED, (omnibuf.nano.v1.field).max_count = 6];
  repeated uint32 rep_expanded = 14 [features.repeated_field_encoding = EXPANDED, (omnibuf.nano.v1.field).max_count = 6];
  repeated Child rep_children = 15 [(omnibuf.nano.v1.field).max_count = 4];

  map<string, int32> map_si = 16 [(omnibuf.nano.v1.field) = {max_count: 4, max_size: 16}];
  map<string, Child> map_sc = 17 [(omnibuf.nano.v1.field) = {max_count: 4, max_size: 16}];

  oneof pick {
    int32 o_i32 = 18;
    bool o_bool = 19;
    string o_str = 20 [(omnibuf.nano.v1.field).max_size = 24];
    Child o_child = 21;
  }
}
`, ed.editionDecl, ed.importLine, ed.extraOptions, ed.exportKw, ed.exportKw, ed.exportKw)

			protoPath := filepath.Join(tmpDir, "ed_conf.proto")
			if err := os.WriteFile(protoPath, []byte(schemaSrc), 0o644); err != nil {
				t.Fatal(err)
			}

			descPath := filepath.Join(tmpDir, "ed_conf.desc.pb")
			protoc := exec.Command("protoc",
				"-I", tmpDir,
				"-I", protoDir,
				"--include_imports",
				"-o", descPath,
				"--plugin=protoc-gen-omnibuf-nano="+pluginBin,
				"--omnibuf-nano_out="+tmpDir,
				protoPath,
			)
			if out, err := protoc.CombinedOutput(); err != nil {
				t.Fatalf("protoc failed: %v\n%s", err, string(out))
			}

			cSrc := `
#include <stdio.h>
#include <stdint.h>
#include "ob_common.h"
#include "ob_decode.h"
#include "ob_encode.h"
#include "ed_conf.ob.h"

int main(void) {
    static uint8_t in_buf[65536];
    static uint8_t out_buf[65536];
    size_t in_len = 0u;
    for (;;) {
        size_t n = fread(in_buf + in_len, 1u, sizeof(in_buf) - in_len, stdin);
        in_len += n;
        if (n == 0u) {
            if (ferror(stdin)) {
                return 2;
            }
            break;
        }
    }

    omnibuf_edtest_EditionsMsg msg = omnibuf_edtest_EditionsMsg_init_zero;
    ob_istream_t is = ob_istream_from_buffer(in_buf, in_len);
    if (!ob_decode(&is, &omnibuf_edtest_EditionsMsg_msg, &msg)) {
        fprintf(stderr, "ob_decode failed: %s\n", is.errmsg ? is.errmsg : "unknown");
        return 1;
    }

    ob_ostream_t os = ob_ostream_from_buffer(out_buf, sizeof(out_buf));
    if (!ob_encode(&os, &omnibuf_edtest_EditionsMsg_msg, &msg)) {
        fprintf(stderr, "ob_encode failed: %s\n", os.errmsg ? os.errmsg : "unknown");
        return 1;
    }

    if (os.bytes_written > 0u) {
        if (fwrite(out_buf, 1u, os.bytes_written, stdout) != os.bytes_written) {
            return 2;
        }
    }
    return 0;
}
`
			cSrcPath := filepath.Join(tmpDir, "ed_roundtrip.c")
			if err := os.WriteFile(cSrcPath, []byte(cSrc), 0o644); err != nil {
				t.Fatal(err)
			}
			cBin := filepath.Join(tmpDir, "ed_roundtrip")
			cc := exec.Command("cc",
				"-Wall", "-Wextra", "-Werror", "-std=c99", "-pedantic",
				"-fsanitize=address,undefined",
				"-I", runtimeDir,
				"-I", tmpDir,
				filepath.Join(runtimeDir, "ob_varint.c"),
				filepath.Join(runtimeDir, "ob_encode.c"),
				filepath.Join(runtimeDir, "ob_decode.c"),
				filepath.Join(tmpDir, "ed_conf.ob.c"),
				cSrcPath,
				"-o", cBin,
			)
			if out, err := cc.CombinedOutput(); err != nil {
				t.Fatalf("cc failed: %v\n%s", err, string(out))
			}

			rawDesc, err := os.ReadFile(descPath)
			if err != nil {
				t.Fatal(err)
			}
			var fds descriptorpb.FileDescriptorSet
			if err := proto.Unmarshal(rawDesc, &fds); err != nil {
				t.Fatal(err)
			}
			files, err := protodesc.NewFiles(&fds)
			if err != nil {
				t.Fatal(err)
			}
			desc, err := files.FindDescriptorByName("omnibuf.edtest.EditionsMsg")
			if err != nil {
				t.Fatal(err)
			}
			md := desc.(protoreflect.MessageDescriptor)
			h := &harness{binPath: cBin, msgDesc: md}

			// Case 1: Absent fields vs explicit zero/false/empty fields.
			t.Run("AbsentVsExplicitZero", func(t *testing.T) {
				absent := dynamicpb.NewMessage(md)
				absentWire, err := (omnibufproto.MarshalOptions{Deterministic: true}).Marshal(absent)
				if err != nil {
					t.Fatal(err)
				}
				cAbsent := h.runCRoundtrip(t, absentWire)
				if len(cAbsent) != 0 {
					t.Fatalf("expected 0 wire bytes for absent message, got %d", len(cAbsent))
				}

				explicitZero := dynamicpb.NewMessage(md)
				fields := md.Fields()
				explicitZero.Set(fields.ByName("exp_i32"), protoreflect.ValueOfInt32(0))
				explicitZero.Set(fields.ByName("exp_u64"), protoreflect.ValueOfUint64(0))
				explicitZero.Set(fields.ByName("exp_bool"), protoreflect.ValueOfBool(false))
				explicitZero.Set(fields.ByName("exp_f32"), protoreflect.ValueOfFloat32(0))
				explicitZero.Set(fields.ByName("exp_f64"), protoreflect.ValueOfFloat64(0))
				explicitZero.Set(fields.ByName("exp_enum"), protoreflect.ValueOfEnum(0))
				explicitZero.Set(fields.ByName("exp_str"), protoreflect.ValueOfString(""))
				explicitZero.Set(fields.ByName("exp_bytes"), protoreflect.ValueOfBytes([]byte{}))
				explicitZero.Set(fields.ByName("o_bool"), protoreflect.ValueOfBool(false))

				zeroWire, err := (omnibufproto.MarshalOptions{Deterministic: true}).Marshal(explicitZero)
				if err != nil {
					t.Fatal(err)
				}
				cZero := h.runCRoundtrip(t, zeroWire)
				if len(cZero) == 0 {
					t.Fatal("expected non-empty wire bytes for explicit zero fields")
				}
				decoded := dynamicpb.NewMessage(md)
				if err := omnibufproto.Unmarshal(cZero, decoded); err != nil {
					t.Fatalf("omnibufproto.Unmarshal failed: %v", err)
				}
				if !proto.Equal(explicitZero, decoded) {
					t.Fatalf("explicit zero mismatch:\norig:    %v\ndecoded: %v", explicitZero, decoded)
				}
			})

			// Case 2: Rich values, unknown open enum, PACKED & EXPANDED, maps, oneof.
			t.Run("RichRoundtrip", func(t *testing.T) {
				orig := dynamicpb.NewMessage(md)
				fields := md.Fields()
				orig.Set(fields.ByName("exp_i32"), protoreflect.ValueOfInt32(-1234))
				orig.Set(fields.ByName("exp_u64"), protoreflect.ValueOfUint64(9999999999))
				orig.Set(fields.ByName("exp_bool"), protoreflect.ValueOfBool(true))
				orig.Set(fields.ByName("exp_f32"), protoreflect.ValueOfFloat32(-3.5))
				orig.Set(fields.ByName("exp_f64"), protoreflect.ValueOfFloat64(2.718281828))
				orig.Set(fields.ByName("exp_enum"), protoreflect.ValueOfEnum(777))
				orig.Set(fields.ByName("exp_str"), protoreflect.ValueOfString("hello-"+ed.name))
				orig.Set(fields.ByName("exp_bytes"), protoreflect.ValueOfBytes([]byte{0xDE, 0xAD, 0xBE, 0xEF}))
				orig.Set(fields.ByName("imp_i32"), protoreflect.ValueOfInt32(88))
				orig.Set(fields.ByName("imp_bool"), protoreflect.ValueOfBool(true))
				orig.Set(fields.ByName("imp_str"), protoreflect.ValueOfString("imp-val"))

				childFD := fields.ByName("child")
				child := dynamicpb.NewMessage(childFD.Message())
				child.Set(childFD.Message().Fields().ByName("id"), protoreflect.ValueOfInt32(0))
				child.Set(childFD.Message().Fields().ByName("tag"), protoreflect.ValueOfString(""))
				orig.Set(childFD, protoreflect.ValueOfMessage(child))

				repP := orig.Mutable(fields.ByName("rep_packed")).List()
				for _, v := range []uint32{0, 42, 65535} {
					repP.Append(protoreflect.ValueOfUint32(v))
				}
				repE := orig.Mutable(fields.ByName("rep_expanded")).List()
				for _, v := range []uint32{1, 2, 3} {
					repE.Append(protoreflect.ValueOfUint32(v))
				}
				repCFD := fields.ByName("rep_children")
				repC := orig.Mutable(repCFD).List()
				c0 := dynamicpb.NewMessage(repCFD.Message())
				c0.Set(repCFD.Message().Fields().ByName("id"), protoreflect.ValueOfInt32(-9))
				c0.Set(repCFD.Message().Fields().ByName("tag"), protoreflect.ValueOfString("rc0"))
				repC.Append(protoreflect.ValueOfMessage(c0))

				mapSI := orig.Mutable(fields.ByName("map_si")).Map()
				mapSI.Set(protoreflect.ValueOfString("a").MapKey(), protoreflect.ValueOfInt32(-1))
				mapSI.Set(protoreflect.ValueOfString("b").MapKey(), protoreflect.ValueOfInt32(0))

				mapSCFD := fields.ByName("map_sc")
				mapSC := orig.Mutable(mapSCFD).Map()
				mv := dynamicpb.NewMessage(mapSCFD.MapValue().Message())
				mv.Set(mapSCFD.MapValue().Message().Fields().ByName("id"), protoreflect.ValueOfInt32(5))
				mv.Set(mapSCFD.MapValue().Message().Fields().ByName("tag"), protoreflect.ValueOfString("mv"))
				mapSC.Set(protoreflect.ValueOfString("k").MapKey(), protoreflect.ValueOfMessage(mv))

				orig.Set(fields.ByName("o_str"), protoreflect.ValueOfString(""))

				goWire, err := (omnibufproto.MarshalOptions{Deterministic: true}).Marshal(orig)
				if err != nil {
					t.Fatal(err)
				}
				cWire := h.runCRoundtrip(t, goWire)
				decoded := dynamicpb.NewMessage(md)
				if err := omnibufproto.Unmarshal(cWire, decoded); err != nil {
					t.Fatalf("omnibufproto.Unmarshal failed: %v", err)
				}
				if !proto.Equal(orig, decoded) {
					t.Fatalf("rich roundtrip mismatch:\norig:    %v\ndecoded: %v", orig, decoded)
				}
			})
		})
	}

	// Verify Edition 2026 DELIMITED submessages (singular & repeated) in C99 produce
	// wire-compatible OB_WIRE_SUBMSG bytes that Go omnibufproto decodes and re-encodes identically.
	t.Run("Edition2026_DelimitedWireCompatWithGo", func(t *testing.T) {
		tmpDir := t.TempDir()
		delimProto := `edition = "2026";
package omnibuf.delim;
import option "omnibuf/nano/v1/options.proto";
option features.default_symbol_visibility = STRICT;

export message Item {
  int32 id = 1;
  string name = 2 [(omnibuf.nano.v1.field).max_size = 24];
}

export message Holder {
  Item single = 1 [features.message_encoding = DELIMITED];
  repeated Item items = 2 [features.message_encoding = DELIMITED, (omnibuf.nano.v1.field).max_count = 4];
}
`
		lpProto := `edition = "2026";
package omnibuf.delim;
import option "omnibuf/nano/v1/options.proto";
option features.default_symbol_visibility = STRICT;

export message Item {
  int32 id = 1;
  string name = 2 [(omnibuf.nano.v1.field).max_size = 24];
}

export message Holder {
  Item single = 1 [features.message_encoding = LENGTH_PREFIXED];
  repeated Item items = 2 [features.message_encoding = LENGTH_PREFIXED, (omnibuf.nano.v1.field).max_count = 4];
}
`
		delimPath := filepath.Join(tmpDir, "delim.proto")
		lpPath := filepath.Join(tmpDir, "lp.proto")
		if err := os.WriteFile(delimPath, []byte(delimProto), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(lpPath, []byte(lpProto), 0o644); err != nil {
			t.Fatal(err)
		}

		// Generate C99 code for DELIMITED schema.
		protocC := exec.Command("protoc",
			"-I", tmpDir,
			"-I", protoDir,
			"--plugin=protoc-gen-omnibuf-nano="+pluginBin,
			"--omnibuf-nano_out="+tmpDir,
			delimPath,
		)
		if out, err := protocC.CombinedOutput(); err != nil {
			t.Fatalf("protoc C delim failed: %v\n%s", err, string(out))
		}

		// Compile LENGTH_PREFIXED descriptor for Go omnibufproto.
		lpDescPath := filepath.Join(tmpDir, "lp.desc.pb")
		protocLP := exec.Command("protoc",
			"-I", tmpDir,
			"-I", protoDir,
			"--include_imports",
			"-o", lpDescPath,
			lpPath,
		)
		if out, err := protocLP.CombinedOutput(); err != nil {
			t.Fatalf("protoc lp desc failed: %v\n%s", err, string(out))
		}

		cSrc := `
#include <stdio.h>
#include <stdint.h>
#include "ob_common.h"
#include "ob_decode.h"
#include "ob_encode.h"
#include "delim.ob.h"

int main(void) {
    static uint8_t in_buf[4096];
    static uint8_t out_buf[4096];
    size_t in_len = 0u;
    for (;;) {
        size_t n = fread(in_buf + in_len, 1u, sizeof(in_buf) - in_len, stdin);
        in_len += n;
        if (n == 0u) {
            break;
        }
    }
    omnibuf_delim_Holder h = omnibuf_delim_Holder_init_zero;
    ob_istream_t is = ob_istream_from_buffer(in_buf, in_len);
    if (!ob_decode(&is, &omnibuf_delim_Holder_msg, &h)) {
        fprintf(stderr, "ob_decode failed: %s\n", is.errmsg ? is.errmsg : "unknown");
        return 1;
    }
    ob_ostream_t os = ob_ostream_from_buffer(out_buf, sizeof(out_buf));
    if (!ob_encode(&os, &omnibuf_delim_Holder_msg, &h)) {
        fprintf(stderr, "ob_encode failed: %s\n", os.errmsg ? os.errmsg : "unknown");
        return 1;
    }
    if (os.bytes_written > 0u) {
        if (fwrite(out_buf, 1u, os.bytes_written, stdout) != os.bytes_written) {
            return 2;
        }
    }
    return 0;
}
`
		cSrcPath := filepath.Join(tmpDir, "delim_rt.c")
		if err := os.WriteFile(cSrcPath, []byte(cSrc), 0o644); err != nil {
			t.Fatal(err)
		}
		cBin := filepath.Join(tmpDir, "delim_rt")
		cc := exec.Command("cc",
			"-Wall", "-Wextra", "-Werror", "-std=c99", "-pedantic",
			"-fsanitize=address,undefined",
			"-I", runtimeDir,
			"-I", tmpDir,
			filepath.Join(runtimeDir, "ob_varint.c"),
			filepath.Join(runtimeDir, "ob_encode.c"),
			filepath.Join(runtimeDir, "ob_decode.c"),
			filepath.Join(tmpDir, "delim.ob.c"),
			cSrcPath,
			"-o", cBin,
		)
		if out, err := cc.CombinedOutput(); err != nil {
			t.Fatalf("cc delim failed: %v\n%s", err, string(out))
		}

		rawDesc, err := os.ReadFile(lpDescPath)
		if err != nil {
			t.Fatal(err)
		}
		var fds descriptorpb.FileDescriptorSet
		if err := proto.Unmarshal(rawDesc, &fds); err != nil {
			t.Fatal(err)
		}
		files, err := protodesc.NewFiles(&fds)
		if err != nil {
			t.Fatal(err)
		}
		desc, err := files.FindDescriptorByName("omnibuf.delim.Holder")
		if err != nil {
			t.Fatal(err)
		}
		md := desc.(protoreflect.MessageDescriptor)
		orig := dynamicpb.NewMessage(md)

		singleFD := md.Fields().ByName("single")
		single := dynamicpb.NewMessage(singleFD.Message())
		single.Set(singleFD.Message().Fields().ByName("id"), protoreflect.ValueOfInt32(0))
		single.Set(singleFD.Message().Fields().ByName("name"), protoreflect.ValueOfString("delim-one"))
		orig.Set(singleFD, protoreflect.ValueOfMessage(single))

		itemsFD := md.Fields().ByName("items")
		items := orig.Mutable(itemsFD).List()
		for i, nm := range []string{"first", "second"} {
			el := dynamicpb.NewMessage(itemsFD.Message())
			el.Set(itemsFD.Message().Fields().ByName("id"), protoreflect.ValueOfInt32(int32(i+10)))
			el.Set(itemsFD.Message().Fields().ByName("name"), protoreflect.ValueOfString(nm))
			items.Append(protoreflect.ValueOfMessage(el))
		}

		goWire, err := (omnibufproto.MarshalOptions{Deterministic: true}).Marshal(orig)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(cBin)
		cmd.Stdin = bytes.NewReader(goWire)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("delim_rt failed: %v\nstderr: %s", err, stderr.String())
		}
		cWire := stdout.Bytes()
		if !bytes.Equal(goWire, cWire) {
			t.Fatalf("DELIMITED C99 wire bytes differ from Go LENGTH_PREFIXED wire bytes:\ngo: %x\nc:  %x", goWire, cWire)
		}

		decoded := dynamicpb.NewMessage(md)
		if err := omnibufproto.Unmarshal(cWire, decoded); err != nil {
			t.Fatalf("Go Unmarshal of C DELIMITED wire bytes failed: %v", err)
		}
		if !proto.Equal(orig, decoded) {
			t.Fatalf("DELIMITED roundtrip mismatch:\norig:    %v\ndecoded: %v", orig, decoded)
		}
	})
}
