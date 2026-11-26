package conformance_test

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omnibuf/omnibuf-go"
)

type corpusFile struct {
	ValidVectors []corpusValidVector  `json:"valid_vectors"`
	InvalidCases []corpusInvalidEntry `json:"invalid_cases"`
}

type corpusValidVector struct {
	Name          string         `json:"name"`
	Category      string         `json:"category"`
	Description   string         `json:"description"`
	Schema        string         `json:"schema"`
	WireHex       string         `json:"wire_hex"`
	WireByteLen   int            `json:"wire_byte_len"`
	PresentFields []int          `json:"present_fields"`
	ExpectedBlind map[string]any `json:"expected_blind"`
}

type corpusInvalidEntry struct {
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description"`
	Schema      string `json:"schema"`
	WireHex     string `json:"wire_hex"`
}

func parseEditionsCorpusFile(t *testing.T, corpusPath string) ([]corpusValidVector, []corpusInvalidEntry) {
	t.Helper()
	raw, err := os.ReadFile(corpusPath)
	if err != nil {
		t.Fatalf("read corpus %s: %v", corpusPath, err)
	}
	var cf corpusFile
	if err := json.Unmarshal(raw, &cf); err != nil {
		t.Fatalf("parse corpus %s: %v", corpusPath, err)
	}

	var valid []corpusValidVector
	for _, v := range cf.ValidVectors {
		if v.Category == "P_ProtobufEditions" {
			valid = append(valid, v)
		}
	}

	wantInvalid := map[string]bool{
		"editions_2023_invalid_utf8":              true,
		"editions_2024_invalid_utf8":              true,
		"editions_2026_invalid_utf8":              true,
		"editions_2026_truncated_delimited_child": true,
	}
	var invalid []corpusInvalidEntry
	for _, ic := range cf.InvalidCases {
		if wantInvalid[ic.Name] {
			invalid = append(invalid, ic)
		}
	}

	if len(valid) < 15 {
		t.Fatalf("expected at least 15 valid P_ProtobufEditions vectors from %s, got %d", corpusPath, len(valid))
	}
	if len(invalid) != 4 {
		t.Fatalf("expected 4 invalid Editions cases from %s, got %d", corpusPath, len(invalid))
	}
	return valid, invalid
}

func loadCanonicalEditionsCorpus(t *testing.T, rootDir string) ([]corpusValidVector, []corpusInvalidEntry) {
	t.Helper()

	localPath := filepath.Join(rootDir, "tests", "conformance", "testdata", "editions_corpus.json")
	localValid, localInvalid := parseEditionsCorpusFile(t, localPath)

	siblingPath := filepath.Join(rootDir, "..", "omnibuf-go", "testdata", "conformance_corpus.json")
	if info, err := os.Stat(siblingPath); err == nil && !info.IsDir() {
		return parseEditionsCorpusFile(t, siblingPath)
	}
	return localValid, localInvalid
}

func expectedCanonicalReencodeBlind(vec corpusValidVector) map[string]any {
	out := make(map[string]any)
	for k, v := range vec.ExpectedBlind {
		// Implicit scalar defaults (#7=0, #8=false) and empty repeated/map containers
		// (#11=[], #12=[], #13={}) are omitted by canonical C99 ob_encode.
		switch k {
		case "field_7":
			if n, ok := v.(float64); ok && n == 0 {
				continue
			}
		case "field_8":
			if b, ok := v.(bool); ok && !b {
				continue
			}
		case "field_11", "field_12":
			if arr, ok := v.([]any); ok && len(arr) == 0 {
				continue
			}
		case "field_13":
			if mp, ok := v.(map[string]any); ok && len(mp) == 0 {
				continue
			}
		}
		out[k] = v
	}

	// For oneof last-wins vectors, only the winning branch (#17) survives decode/re-encode.
	if strings.HasSuffix(vec.Name, "_oneof_last_wins_and_child") {
		delete(out, "field_14")
		delete(out, "field_15")
	}
	return out
}

func TestCanonicalEditionsCorpus(t *testing.T) {
	rootDir, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	schemaDir := filepath.Join(rootDir, "tests", "conformance", "schema")
	protoDir := filepath.Join(rootDir, "proto")
	runtimeDir := filepath.Join(rootDir, "runtime")
	tmpDir := t.TempDir()

	validVectors, invalidCases := loadCanonicalEditionsCorpus(t, rootDir)

	// 1. Build protoc-gen-omnibuf-nano.
	pluginBin := filepath.Join(tmpDir, "protoc-gen-omnibuf-nano")
	buildPlugin := exec.Command("go", "build", "-o", pluginBin, "./cmd/protoc-gen-omnibuf-nano")
	buildPlugin.Dir = rootDir
	if out, err := buildPlugin.CombinedOutput(); err != nil {
		t.Fatalf("build plugin: %v\n%s", err, string(out))
	}

	// 2. Compile canonical editions_2023.proto, editions_2024.proto, and editions_2026.proto
	// via protoc-gen-omnibuf-nano using sidecar .options files.
	schemas := []string{"editions_2023.proto", "editions_2024.proto", "editions_2026.proto"}
	args := []string{
		"-I", schemaDir,
		"-I", protoDir,
		"--plugin=protoc-gen-omnibuf-nano=" + pluginBin,
		"--omnibuf-nano_out=" + tmpDir,
		"--omnibuf-nano_opt=options_path=" + schemaDir,
	}
	for _, s := range schemas {
		args = append(args, filepath.Join(schemaDir, s))
	}
	protocCmd := exec.Command("protoc", args...)
	if out, err := protocCmd.CombinedOutput(); err != nil {
		t.Fatalf("protoc canonical editions schemas failed: %v\n%s", err, string(out))
	}

	// 3. Write strict C99 corpus roundtrip runner supporting all 3 canonical schemas.
	driverSrc := `
#include <assert.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

#include "ob_common.h"
#include "ob_decode.h"
#include "ob_encode.h"
#include "editions_2023.ob.h"
#include "editions_2024.ob.h"
#include "editions_2026.ob.h"

typedef char _chk_e23_sz[(sizeof(omnibuf_conformance_Editions2023Status) == 4u) ? 1 : -1];
typedef char _chk_e23_sg[(((omnibuf_conformance_Editions2023Status)-1) < 0) ? 1 : -1];
typedef char _chk_e24_sz[(sizeof(omnibuf_conformance_Editions2024Status) == 4u) ? 1 : -1];
typedef char _chk_e24_sg[(((omnibuf_conformance_Editions2024Status)-1) < 0) ? 1 : -1];
typedef char _chk_e26_sz[(sizeof(omnibuf_conformance_Editions2026Status) == 4u) ? 1 : -1];
typedef char _chk_e26_sg[(((omnibuf_conformance_Editions2026Status)-1) < 0) ? 1 : -1];

static int run_schema(const ob_msgdesc_t *desc, const uint8_t *in_buf, size_t in_len) {
    static uint8_t msg1[16384];
    static uint8_t msg2[16384];
    static uint8_t out1[16384];
    static uint8_t out2[16384];

    if (desc->struct_size > sizeof(msg1)) {
        fprintf(stderr, "struct too large\n");
        return 2;
    }

    /* Poison destination structs before ob_decode to verify zero-init of all absent fields. */
    memset(msg1, 0xAA, desc->struct_size);
    ob_istream_t is1 = ob_istream_from_buffer(in_buf, in_len);
    if (!ob_decode(&is1, desc, msg1)) {
        fprintf(stderr, "ob_decode failed: %s\n", is1.errmsg ? is1.errmsg : "unknown");
        return 1;
    }

    ob_ostream_t os1 = ob_ostream_from_buffer(out1, sizeof(out1));
    if (!ob_encode(&os1, desc, msg1)) {
        fprintf(stderr, "ob_encode failed: %s\n", os1.errmsg ? os1.errmsg : "unknown");
        return 1;
    }

    /* Verify idempotent second decode -> encode cycle produces identical wire bytes. */
    memset(msg2, 0x55, desc->struct_size);
    ob_istream_t is2 = ob_istream_from_buffer(out1, os1.bytes_written);
    if (!ob_decode(&is2, desc, msg2)) {
        fprintf(stderr, "second ob_decode failed: %s\n", is2.errmsg ? is2.errmsg : "unknown");
        return 2;
    }
    ob_ostream_t os2 = ob_ostream_from_buffer(out2, sizeof(out2));
    if (!ob_encode(&os2, desc, msg2)) {
        fprintf(stderr, "second ob_encode failed: %s\n", os2.errmsg ? os2.errmsg : "unknown");
        return 2;
    }
    if (os1.bytes_written != os2.bytes_written ||
        (os1.bytes_written > 0u && memcmp(out1, out2, os1.bytes_written) != 0)) {
        fprintf(stderr, "non-idempotent re-encode\n");
        return 2;
    }

    if (os1.bytes_written > 0u) {
        if (fwrite(out1, 1u, os1.bytes_written, stdout) != os1.bytes_written) {
            fprintf(stderr, "stdout write error\n");
            return 2;
        }
    }
    return 0;
}

int main(int argc, char **argv) {
    static uint8_t in_buf[16384];
    if (argc != 2) {
        fprintf(stderr, "usage: %s <schema>\n", argv[0]);
        return 2;
    }

    const ob_msgdesc_t *desc = NULL;
    if (strcmp(argv[1], "TestEditions2023") == 0) {
        desc = &omnibuf_conformance_TestEditions2023_msg;
    } else if (strcmp(argv[1], "TestEditions2024") == 0) {
        desc = &omnibuf_conformance_TestEditions2024_msg;
    } else if (strcmp(argv[1], "TestEditions2026") == 0) {
        desc = &omnibuf_conformance_TestEditions2026_msg;
    } else {
        fprintf(stderr, "unknown schema: %s\n", argv[1]);
        return 2;
    }

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
        if (in_len == sizeof(in_buf)) {
            fprintf(stderr, "input exceeds buffer\n");
            return 2;
        }
    }

    return run_schema(desc, in_buf, in_len);
}
`
	driverPath := filepath.Join(tmpDir, "editions_corpus_rt.c")
	if err := os.WriteFile(driverPath, []byte(driverSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	cSources := []string{
		filepath.Join(runtimeDir, "ob_varint.c"),
		filepath.Join(runtimeDir, "ob_encode.c"),
		filepath.Join(runtimeDir, "ob_decode.c"),
		filepath.Join(tmpDir, "editions_2023.ob.c"),
		filepath.Join(tmpDir, "editions_2024.ob.c"),
		filepath.Join(tmpDir, "editions_2026.ob.c"),
		driverPath,
	}

	// 4. Verify strict C99 syntax check under -fshort-enums.
	shortEnumArgs := append([]string{
		"-fsyntax-only",
		"-Wall", "-Wextra", "-Werror", "-std=c99", "-pedantic", "-fshort-enums",
		"-I", runtimeDir,
		"-I", tmpDir,
	}, cSources...)
	if out, err := exec.Command("cc", shortEnumArgs...).CombinedOutput(); err != nil {
		t.Fatalf("C99 -fshort-enums check failed: %v\n%s", err, string(out))
	}

	// 5. Compile executable with strict C99 flags + ASan + UBSan.
	cBin := filepath.Join(tmpDir, "editions_corpus_rt")
	compileArgs := append([]string{
		"-Wall", "-Wextra", "-Werror", "-std=c99", "-pedantic",
		"-Wshadow", "-Wundef", "-Wcast-align", "-Wstrict-aliasing=2", "-Wdouble-promotion",
		"-fsanitize=address,undefined",
		"-I", runtimeDir,
		"-I", tmpDir,
	}, cSources...)
	compileArgs = append(compileArgs, "-o", cBin)
	if out, err := exec.Command("cc", compileArgs...).CombinedOutput(); err != nil {
		t.Fatalf("C99 corpus runner compile failed: %v\n%s", err, string(out))
	}

	// 6. Execute all 15 canonical valid Editions vectors.
	t.Run("ValidVectors", func(t *testing.T) {
		for _, vec := range validVectors {
			t.Run(vec.Name, func(t *testing.T) {
				wireIn, err := hex.DecodeString(vec.WireHex)
				if err != nil {
					t.Fatalf("decode wire_hex: %v", err)
				}
				if len(wireIn) != vec.WireByteLen {
					t.Fatalf("wire_byte_len mismatch: got %d, want %d", len(wireIn), vec.WireByteLen)
				}

				cmd := exec.Command(cBin, vec.Schema)
				cmd.Stdin = bytes.NewReader(wireIn)
				var stdout, stderr bytes.Buffer
				cmd.Stdout = &stdout
				cmd.Stderr = &stderr
				if err := cmd.Run(); err != nil {
					t.Fatalf("C99 decode/encode failed for %s (%s): %v\nstderr: %s",
						vec.Name, vec.Schema, err, stderr.String())
				}
				cWire := stdout.Bytes()

				// Empty and full_and_open_enum vectors must match wire_hex byte-for-byte.
				if strings.HasSuffix(vec.Name, "_empty") || strings.HasSuffix(vec.Name, "_full_and_open_enum") {
					if !bytes.Equal(cWire, wireIn) {
						t.Fatalf("wire mismatch for %s:\ngot:  %x\nwant: %x", vec.Name, cWire, wireIn)
					}
				}

				// Blind-decode C99 output and verify exact canonical semantic fidelity.
				gotJSON, err := omnibuf.DecodeBlindJSON(cWire)
				if err != nil {
					t.Fatalf("DecodeBlindJSON of C99 output failed for %s: %v (wire=%x)", vec.Name, err, cWire)
				}
				var gotMap map[string]any
				if err := json.Unmarshal([]byte(gotJSON), &gotMap); err != nil {
					t.Fatalf("unmarshal blind JSON failed: %v", err)
				}
				wantMap := expectedCanonicalReencodeBlind(vec)
				if !reflect.DeepEqual(gotMap, wantMap) {
					t.Fatalf("semantic mismatch after C99 decode -> re-encode for %s:\ngot:  %#v\nwant: %#v\nwire: %x",
						vec.Name, gotMap, wantMap, cWire)
				}
			})
		}
	})

	// 7. Execute all 4 canonical invalid Editions cases and verify C99 rejects every one.
	t.Run("InvalidCases", func(t *testing.T) {
		for _, ic := range invalidCases {
			t.Run(ic.Name, func(t *testing.T) {
				wireIn, err := hex.DecodeString(ic.WireHex)
				if err != nil {
					t.Fatalf("decode wire_hex: %v", err)
				}
				cmd := exec.Command(cBin, ic.Schema)
				cmd.Stdin = bytes.NewReader(wireIn)
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				if err := cmd.Run(); err == nil {
					t.Fatalf("expected C99 to reject invalid case %s (%s), but decode succeeded",
						ic.Name, ic.Description)
				}
			})
		}
	})
}
