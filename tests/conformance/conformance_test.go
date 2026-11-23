package conformance_test

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/omnibuf/omnibuf-go/omnibufproto"
	"github.com/omnibuf/omnibuf-go/wire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

type harness struct {
	binPath string
	msgDesc protoreflect.MessageDescriptor
}

func setupHarness(t *testing.T) *harness {
	t.Helper()

	rootDir, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	schemaDir := filepath.Join(rootDir, "tests", "conformance", "schema")
	protoDir := filepath.Join(rootDir, "proto")
	runtimeDir := filepath.Join(rootDir, "runtime")
	tmpDir := t.TempDir()

	// 1. Build protoc-gen-omnibuf-nano plugin binary.
	pluginBin := filepath.Join(tmpDir, "protoc-gen-omnibuf-nano")
	buildPlugin := exec.Command("go", "build", "-o", pluginBin, "./cmd/protoc-gen-omnibuf-nano")
	buildPlugin.Dir = rootDir
	if out, err := buildPlugin.CombinedOutput(); err != nil {
		t.Fatalf("build plugin: %v\n%s", err, string(out))
	}

	// 2. Run protoc to generate both FileDescriptorSet (for Go dynamicpb) and C .ob.h/.ob.c.
	descPath := filepath.Join(tmpDir, "conformance.desc.pb")
	protoc := exec.Command("protoc",
		"-I", schemaDir,
		"-I", protoDir,
		"--include_imports",
		"-o", descPath,
		"--plugin=protoc-gen-omnibuf-nano="+pluginBin,
		"--omnibuf-nano_out="+tmpDir,
		"--omnibuf-nano_opt=options_path="+schemaDir,
		filepath.Join(schemaDir, "conformance.proto"),
	)
	if out, err := protoc.CombinedOutput(); err != nil {
		t.Fatalf("protoc codegen: %v\n%s", err, string(out))
	}

	// 3. Compile C99 roundtrip binary with strict C99 + ASan/UBSan.
	cBin := filepath.Join(tmpDir, "c_roundtrip")
	cc := exec.Command("cc",
		"-Wall", "-Wextra", "-Werror", "-std=c99", "-pedantic",
		"-fsanitize=address,undefined",
		"-I", runtimeDir,
		"-I", tmpDir,
		filepath.Join(runtimeDir, "ob_varint.c"),
		filepath.Join(runtimeDir, "ob_encode.c"),
		filepath.Join(runtimeDir, "ob_decode.c"),
		filepath.Join(tmpDir, "conformance.ob.c"),
		filepath.Join(rootDir, "tests", "conformance", "c_src", "c_roundtrip.c"),
		"-o", cBin,
	)
	if out, err := cc.CombinedOutput(); err != nil {
		t.Fatalf("compile C roundtrip binary: %v\n%s", err, string(out))
	}

	// 4. Load FileDescriptorSet for Go dynamicpb + omnibufproto.
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
	desc, err := files.FindDescriptorByName("omnibuf.nano.test.ConformanceMsg")
	if err != nil {
		t.Fatal(err)
	}
	return &harness{
		binPath: cBin,
		msgDesc: desc.(protoreflect.MessageDescriptor),
	}
}

func (h *harness) runCRoundtrip(t *testing.T, input []byte) []byte {
	t.Helper()
	cmd := exec.Command(h.binPath)
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("c_roundtrip failed: %v\nstderr: %s", err, stderr.String())
	}
	return stdout.Bytes()
}

func buildFullMessage(md protoreflect.MessageDescriptor) *dynamicpb.Message {
	msg := dynamicpb.NewMessage(md)
	fields := md.Fields()
	set := func(name protoreflect.Name, v protoreflect.Value) {
		msg.Set(fields.ByName(name), v)
	}

	set("f_uint32", protoreflect.ValueOfUint32(42000))
	set("f_uint64", protoreflect.ValueOfUint64(18446744073709551615))
	set("f_int32", protoreflect.ValueOfInt32(-123456))
	set("f_int64", protoreflect.ValueOfInt64(-98765432109876))
	set("f_sint32", protoreflect.ValueOfInt32(-42))
	set("f_sint64", protoreflect.ValueOfInt64(math.MinInt64))
	set("f_fixed32", protoreflect.ValueOfUint32(0xDEADBEEF))
	set("f_fixed64", protoreflect.ValueOfUint64(0xCAFEBABEDEADBEEF))
	set("f_sfixed32", protoreflect.ValueOfInt32(-777777))
	set("f_sfixed64", protoreflect.ValueOfInt64(-8888888888888))
	set("f_float", protoreflect.ValueOfFloat32(-12.5))
	set("f_double", protoreflect.ValueOfFloat64(3.141592653589793))
	set("f_bool", protoreflect.ValueOfBool(true))
	set("f_enum", protoreflect.ValueOfEnum(2))
	set("f_string", protoreflect.ValueOfString("hello omnibuf-nano!"))
	set("f_bytes", protoreflect.ValueOfBytes([]byte{0x01, 0x02, 0x03, 0xFE, 0xFF}))
	set("f_fixed_bytes", protoreflect.ValueOfBytes([]byte{0x10, 0x20, 0x30, 0x40, 0x50, 0x60, 0x70, 0x80}))
	set("f_opt_bool", protoreflect.ValueOfBool(false))
	set("f_opt_int32", protoreflect.ValueOfInt32(0))

	nestedFD := fields.ByName("f_nested")
	nested := dynamicpb.NewMessage(nestedFD.Message())
	nested.Set(nestedFD.Message().Fields().ByName("id"), protoreflect.ValueOfInt32(-99))
	nested.Set(nestedFD.Message().Fields().ByName("label"), protoreflect.ValueOfString("inner-node"))
	nested.Set(nestedFD.Message().Fields().ByName("active"), protoreflect.ValueOfBool(true))
	msg.Set(nestedFD, protoreflect.ValueOfMessage(nested))

	repU32 := msg.Mutable(fields.ByName("f_rep_uint32")).List()
	for _, v := range []uint32{0, 1, 127, 128, 16384, 536870912} {
		repU32.Append(protoreflect.ValueOfUint32(v))
	}

	repStr := msg.Mutable(fields.ByName("f_rep_string")).List()
	for _, s := range []string{"alpha", "", "gamma"} {
		repStr.Append(protoreflect.ValueOfString(s))
	}

	repBytes := msg.Mutable(fields.ByName("f_rep_bytes")).List()
	repBytes.Append(protoreflect.ValueOfBytes([]byte{0xAA, 0xBB}))
	repBytes.Append(protoreflect.ValueOfBytes([]byte{}))
	repBytes.Append(protoreflect.ValueOfBytes([]byte{0xCC}))

	repNestedFD := fields.ByName("f_rep_nested")
	repNested := msg.Mutable(repNestedFD).List()
	for i, lbl := range []string{"elem0", "elem1"} {
		el := dynamicpb.NewMessage(repNestedFD.Message())
		el.Set(repNestedFD.Message().Fields().ByName("id"), protoreflect.ValueOfInt32(int32(i+10)))
		el.Set(repNestedFD.Message().Fields().ByName("label"), protoreflect.ValueOfString(lbl))
		el.Set(repNestedFD.Message().Fields().ByName("active"), protoreflect.ValueOfBool(i%2 == 0))
		repNested.Append(protoreflect.ValueOfMessage(el))
	}

	// Set oneof arm (string).
	set("oneof_str", protoreflect.ValueOfString("active-arm"))

	// Map<string, uint64>
	mapStrU64 := msg.Mutable(fields.ByName("f_map_str_u64")).Map()
	mapStrU64.Set(protoreflect.ValueOfString("k1").MapKey(), protoreflect.ValueOfUint64(111))
	mapStrU64.Set(protoreflect.ValueOfString("k2").MapKey(), protoreflect.ValueOfUint64(9999999999))

	// Map<int32, Nested>
	mapI32MsgFD := fields.ByName("f_map_i32_msg")
	mapI32Msg := msg.Mutable(mapI32MsgFD).Map()
	valDesc := mapI32MsgFD.MapValue().Message()
	mv := dynamicpb.NewMessage(valDesc)
	mv.Set(valDesc.Fields().ByName("id"), protoreflect.ValueOfInt32(77))
	mv.Set(valDesc.Fields().ByName("label"), protoreflect.ValueOfString("mapped"))
	mapI32Msg.Set(protoreflect.ValueOfInt32(-5).MapKey(), protoreflect.ValueOfMessage(mv))

	return msg
}

func appendUnknownFields(dst []byte) []byte {
	writeTag := func(b []byte, fnum uint32, wt uint8) []byte {
		return wire.AppendVarint(b, (uint64(fnum)<<4)|uint64(wt))
	}
	// Unknown WireUint
	dst = writeTag(dst, 200, wire.WireUint)
	dst = wire.AppendVarint(dst, 987654321)
	// Unknown WireSint
	dst = writeTag(dst, 201, wire.WireSint)
	dst = wire.AppendVarint(dst, wire.EncodeZigZag(-54321))
	// Unknown WireBoolFalse & WireBoolTrue
	dst = writeTag(dst, 202, wire.WireBoolFalse)
	dst = writeTag(dst, 203, wire.WireBoolTrue)
	// Unknown WireFixed32 / Sfixed32 / Float32
	for i, wt := range []uint8{wire.WireFixed32, wire.WireSfixed32, wire.WireFloat32} {
		dst = writeTag(dst, uint32(204+i), wt)
		dst = binary.LittleEndian.AppendUint32(dst, 0x11223344)
	}
	// Unknown WireFixed64 / Sfixed64 / Float64
	for i, wt := range []uint8{wire.WireFixed64, wire.WireSfixed64, wire.WireFloat64} {
		dst = writeTag(dst, uint32(207+i), wt)
		dst = binary.LittleEndian.AppendUint64(dst, 0x1122334455667788)
	}
	// Unknown WireString
	dst = writeTag(dst, 210, wire.WireString)
	dst = wire.AppendVarint(dst, 11)
	dst = append(dst, "unknown-str"...)
	// Unknown WireBytes
	dst = writeTag(dst, 211, wire.WireBytes)
	dst = wire.AppendVarint(dst, 4)
	dst = append(dst, 0xDE, 0xAD, 0xBE, 0xEF)
	// Unknown WireSubMsg
	dst = writeTag(dst, 212, wire.WireSubMsg)
	dst = wire.AppendVarint(dst, 2)
	dst = append(dst, 0x11, 0x03) // dummy inner field
	// Unknown WireArray
	dst = writeTag(dst, 213, wire.WireArray)
	dst = wire.EncodeUintArr(dst, []uint32{1, 2, 3})
	// Unknown WireMap
	dst = writeTag(dst, 214, wire.WireMap)
	dst = wire.EncodeMapStringUint(dst, map[string]uint64{"x": 42}, true)
	// Unknown WireExtended
	dst = writeTag(dst, 215, wire.WireExtended)
	dst = wire.AppendVarint(dst, 3)
	dst = append(dst, 0x01, 0x02, 0x03)
	return dst
}

func TestCrossLanguageConformance(t *testing.T) {
	h := setupHarness(t)

	t.Run("FullRoundtrip", func(t *testing.T) {
		orig := buildFullMessage(h.msgDesc)
		goWire, err := (omnibufproto.MarshalOptions{Deterministic: true}).Marshal(orig)
		if err != nil {
			t.Fatalf("Go omnibufproto.Marshal failed: %v", err)
		}

		cWire := h.runCRoundtrip(t, goWire)

		decoded := dynamicpb.NewMessage(h.msgDesc)
		if err := omnibufproto.Unmarshal(cWire, decoded); err != nil {
			t.Fatalf("Go omnibufproto.Unmarshal of C output failed: %v", err)
		}
		if !proto.Equal(orig, decoded) {
			t.Fatalf("semantic mismatch after Go -> C -> Go roundtrip:\norig:    %v\ndecoded: %v", orig, decoded)
		}
	})

	t.Run("SkipField0SelfDescribing", func(t *testing.T) {
		orig := buildFullMessage(h.msgDesc)
		goWireWithField0, err := (omnibufproto.MarshalOptions{
			Deterministic:  true,
			SelfDescribing: true,
		}).Marshal(orig)
		if err != nil {
			t.Fatalf("Go SelfDescribing Marshal failed: %v", err)
		}

		cWire := h.runCRoundtrip(t, goWireWithField0)

		decoded := dynamicpb.NewMessage(h.msgDesc)
		if err := omnibufproto.Unmarshal(cWire, decoded); err != nil {
			t.Fatalf("Go Unmarshal after Field #0 skip failed: %v", err)
		}
		if !proto.Equal(orig, decoded) {
			t.Fatalf("semantic mismatch after Field #0 skip:\norig:    %v\ndecoded: %v", orig, decoded)
		}
	})

	t.Run("SkipUnknownFieldsAllWireTypes", func(t *testing.T) {
		orig := buildFullMessage(h.msgDesc)
		goWire, err := (omnibufproto.MarshalOptions{Deterministic: true}).Marshal(orig)
		if err != nil {
			t.Fatalf("Go Marshal failed: %v", err)
		}
		withUnknowns := appendUnknownFields(append([]byte{}, goWire...))

		cWire := h.runCRoundtrip(t, withUnknowns)

		decoded := dynamicpb.NewMessage(h.msgDesc)
		if err := omnibufproto.Unmarshal(cWire, decoded); err != nil {
			t.Fatalf("Go Unmarshal after unknown field skip failed: %v", err)
		}
		if !proto.Equal(orig, decoded) {
			t.Fatalf("semantic mismatch after unknown field skip:\norig:    %v\ndecoded: %v", orig, decoded)
		}
	})

	t.Run("OneofNestedMessageArm", func(t *testing.T) {
		orig := dynamicpb.NewMessage(h.msgDesc)
		// fixed_length bytes field is always serialized by C, so set 8 zero bytes on orig too.
		orig.Set(h.msgDesc.Fields().ByName("f_fixed_bytes"), protoreflect.ValueOfBytes(make([]byte, 8)))
		armFD := h.msgDesc.Fields().ByName("oneof_nested")
		arm := dynamicpb.NewMessage(armFD.Message())
		arm.Set(armFD.Message().Fields().ByName("id"), protoreflect.ValueOfInt32(555))
		arm.Set(armFD.Message().Fields().ByName("label"), protoreflect.ValueOfString("oneof-sub"))
		orig.Set(armFD, protoreflect.ValueOfMessage(arm))

		goWire, err := (omnibufproto.MarshalOptions{Deterministic: true}).Marshal(orig)
		if err != nil {
			t.Fatalf("Go Marshal failed: %v", err)
		}
		cWire := h.runCRoundtrip(t, goWire)
		decoded := dynamicpb.NewMessage(h.msgDesc)
		if err := omnibufproto.Unmarshal(cWire, decoded); err != nil {
			t.Fatalf("Go Unmarshal failed: %v", err)
		}
		if !proto.Equal(orig, decoded) {
			t.Fatalf("oneof nested arm mismatch:\norig:    %v\ndecoded: %v", orig, decoded)
		}
	})
}
