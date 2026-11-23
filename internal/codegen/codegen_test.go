package codegen_test

import (
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

func compileProtoDescriptor(t *testing.T, protoFiles map[string]string, target string) protoreflect.FileDescriptor {
	t.Helper()
	dir := t.TempDir()
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
	descOut := filepath.Join(dir, "desc.pb")
	cmd := exec.Command("protoc",
		"-I", dir,
		"-I", protoDir,
		"--include_imports",
		"-o", descOut,
		filepath.Join(dir, target),
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("protoc failed: %v\n%s", err, string(out))
	}

	raw, err := os.ReadFile(descOut)
	if err != nil {
		t.Fatal(err)
	}
	var fds descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(raw, &fds); err != nil {
		t.Fatal(err)
	}
	files, err := protodesc.NewFiles(&fds)
	if err != nil {
		t.Fatal(err)
	}
	fd, err := files.FindFileByPath(target)
	if err != nil {
		t.Fatal(err)
	}
	return fd
}

func TestGenerateAndCompileC99(t *testing.T) {
	fd := compileProtoDescriptor(t, map[string]string{
		"sample.proto": `
syntax = "proto3";
package demo;
import "omnibuf/nano/v1/options.proto";

enum Mode {
  MODE_UNSPECIFIED = 0;
  MODE_FAST = 1;
}

message Ignored {
  option (omnibuf.nano.v1.message).skip_message = true;
  string unbounded = 1;
}

message Child {
  int32 x = 1;
  string tag = 2 [(omnibuf.nano.v1.field).max_size = 16];
}

message Parent {
  Child child = 1;
  Mode mode = 2;
  string name = 3 [(omnibuf.nano.v1.field).max_size = 8]; // overridden by sidecar to 32
  bytes fixed_id = 4 [(omnibuf.nano.v1.field) = {max_size: 4, fixed_length: true}];
  repeated uint32 scores = 5 [(omnibuf.nano.v1.field).max_count = 4];
  oneof pick {
    int32 a = 6;
    string b = 7 [(omnibuf.nano.v1.field).max_size = 16];
  }
  map<string, uint64> labels = 8 [(omnibuf.nano.v1.field) = {max_count: 4, max_size: 16}];
}
`,
	}, "sample.proto")

	sidecar, err := options.Parse(strings.NewReader("Parent.name max_size:32\n"))
	if err != nil {
		t.Fatal(err)
	}

	out, err := codegen.Generate(fd, codegen.Config{Sidecar: sidecar})
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 generated files, got %d", len(out))
	}

	header := out[0].Content
	source := out[1].Content

	if strings.Contains(header, "Ignored") {
		t.Errorf("skip_message did not omit Ignored from header")
	}
	if !strings.Contains(header, "char name[32u];") {
		t.Errorf("sidecar override did not apply max_size:32 to Parent.name:\n%s", header)
	}
	if !strings.Contains(header, "uint8_t fixed_id[4u];") {
		t.Errorf("fixed_length bytes did not emit raw uint8_t array:\n%s", header)
	}

	// Verify generated C code compiles cleanly under strict C99.
	tmpDir := t.TempDir()
	for _, gf := range out {
		if err := os.WriteFile(filepath.Join(tmpDir, gf.Path), []byte(gf.Content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runtimeDir, err := filepath.Abs("../../runtime")
	if err != nil {
		t.Fatal(err)
	}
	objOut := filepath.Join(tmpDir, "sample.ob.o")
	cc := exec.Command("cc",
		"-Wall", "-Wextra", "-Werror", "-std=c99", "-pedantic",
		"-c",
		"-I", runtimeDir,
		"-I", tmpDir,
		filepath.Join(tmpDir, "sample.ob.c"),
		"-o", objOut,
	)
	if ccOut, err := cc.CombinedOutput(); err != nil {
		t.Fatalf("generated C failed strict C99 compile: %v\n%s\n--- header ---\n%s\n--- source ---\n%s",
			err, string(ccOut), header, source)
	}
}

func TestUnboundedFieldErrors(t *testing.T) {
	cases := []struct {
		name  string
		field string
	}{
		{"string", "string s = 1;"},
		{"bytes", "bytes b = 1;"},
		{"repeated", "repeated uint32 r = 1;"},
		{"map", "map<string, uint32> m = 1;"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fd := compileProtoDescriptor(t, map[string]string{
				"bad.proto": "syntax = \"proto3\";\nmessage Bad { " + tc.field + " }\n",
			}, "bad.proto")
			if _, err := codegen.Generate(fd, codegen.Config{}); err == nil {
				t.Fatalf("expected codegen error for unbounded %s field, got nil", tc.name)
			}
		})
	}
}
