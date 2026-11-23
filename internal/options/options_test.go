package options

import (
	"strings"
	"testing"
)

func TestParseAndResolve(t *testing.T) {
	input := `
# Global defaults
*.* max_size:32 max_count:8
// Specific overrides
MyMessage.name max_size:64
MyMessage.uuid max_size:16 fixed_length:true
pkg.SecretMessage skip_message:true
*.tags max_count:16 # trailing comment
`
	f, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	got := f.Resolve("MyMessage.name", "pkg.MyMessage.name")
	if !got.HasMaxSize || got.MaxSize != 64 || !got.HasMaxCount || got.MaxCount != 8 {
		t.Fatalf("unexpected MyMessage.name opts: %+v", got)
	}

	uuid := f.Resolve("MyMessage.uuid")
	if !uuid.HasMaxSize || uuid.MaxSize != 16 || !uuid.HasFixedLength || !uuid.FixedLength {
		t.Fatalf("unexpected MyMessage.uuid opts: %+v", uuid)
	}

	tags := f.Resolve("Other.tags")
	if !tags.HasMaxCount || tags.MaxCount != 16 || !tags.HasMaxSize || tags.MaxSize != 32 {
		t.Fatalf("unexpected Other.tags opts: %+v", tags)
	}

	skip := f.Resolve("SecretMessage", "pkg.SecretMessage")
	if !skip.HasSkipMessage || !skip.SkipMessage {
		t.Fatalf("unexpected SecretMessage opts: %+v", skip)
	}
}

func TestMergePrecedence(t *testing.T) {
	fileDefault := Options{MaxSize: 16, HasMaxSize: true, MaxCount: 4, HasMaxCount: true}
	protoAnnot := Options{MaxSize: 32, HasMaxSize: true}
	sidecar := Options{MaxCount: 12, HasMaxCount: true, FixedLength: true, HasFixedLength: true}

	merged := Merge(Merge(fileDefault, protoAnnot), sidecar)
	if merged.MaxSize != 32 || merged.MaxCount != 12 || !merged.FixedLength {
		t.Fatalf("unexpected merged opts: %+v", merged)
	}
}

func TestParseErrors(t *testing.T) {
	badInputs := []string{
		"MyMessage.field",
		"MyMessage.field unknown_opt:1",
		"MyMessage.field max_size:abc",
		"MyMessage.field fixed_length:notabool",
	}
	for _, in := range badInputs {
		if _, err := Parse(strings.NewReader(in)); err == nil {
			t.Errorf("expected error for %q, got nil", in)
		}
	}
}
