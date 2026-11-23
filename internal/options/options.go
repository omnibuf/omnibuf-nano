package options

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Options holds resolved omnibuf-nano static bounds and codegen flags.
type Options struct {
	MaxSize        uint32
	HasMaxSize     bool
	MaxCount       uint32
	HasMaxCount    bool
	FixedLength    bool
	HasFixedLength bool
	SkipMessage    bool
	HasSkipMessage bool
}

// Merge returns a copy of base with any explicitly set fields from override applied.
func Merge(base, override Options) Options {
	out := base
	if override.HasMaxSize {
		out.MaxSize = override.MaxSize
		out.HasMaxSize = true
	}
	if override.HasMaxCount {
		out.MaxCount = override.MaxCount
		out.HasMaxCount = true
	}
	if override.HasFixedLength {
		out.FixedLength = override.FixedLength
		out.HasFixedLength = true
	}
	if override.HasSkipMessage {
		out.SkipMessage = override.SkipMessage
		out.HasSkipMessage = true
	}
	return out
}

// Rule is a single pattern-to-options entry from a sidecar .options file.
type Rule struct {
	Pattern string
	Opts    Options
}

// File is a parsed sidecar .options file containing ordered wildcard rules.
type File struct {
	Rules []Rule
}

// ParseFile reads and parses a sidecar .options file from disk.
func ParseFile(path string) (*File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	parsed, err := Parse(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return parsed, nil
}

// Parse reads nanopb-style sidecar .options rules from r.
// Syntax: <glob_pattern> <key>:<value> [<key>:<value> ...]
// Lines starting with # or // (and trailing # or // comments) are ignored.
func Parse(r io.Reader) (*File, error) {
	out := &File{}
	scanner := bufio.NewScanner(r)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := stripComment(scanner.Text())
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return nil, fmt.Errorf("line %d: expected '<pattern> <option>:<value>'", lineNum)
		}
		rule := Rule{Pattern: fields[0]}
		for _, tok := range fields[1:] {
			key, val, hasColon := strings.Cut(tok, ":")
			if !hasColon {
				// Allow bare boolean flags like "fixed_length" or "skip_message".
				val = "true"
			}
			switch key {
			case "max_size":
				n, err := strconv.ParseUint(val, 10, 32)
				if err != nil {
					return nil, fmt.Errorf("line %d: invalid max_size %q: %w", lineNum, val, err)
				}
				rule.Opts.MaxSize = uint32(n)
				rule.Opts.HasMaxSize = true
			case "max_count":
				n, err := strconv.ParseUint(val, 10, 32)
				if err != nil {
					return nil, fmt.Errorf("line %d: invalid max_count %q: %w", lineNum, val, err)
				}
				rule.Opts.MaxCount = uint32(n)
				rule.Opts.HasMaxCount = true
			case "fixed_length":
				b, err := strconv.ParseBool(val)
				if err != nil {
					return nil, fmt.Errorf("line %d: invalid fixed_length %q: %w", lineNum, val, err)
				}
				rule.Opts.FixedLength = b
				rule.Opts.HasFixedLength = true
			case "skip_message":
				b, err := strconv.ParseBool(val)
				if err != nil {
					return nil, fmt.Errorf("line %d: invalid skip_message %q: %w", lineNum, val, err)
				}
				rule.Opts.SkipMessage = b
				rule.Opts.HasSkipMessage = true
			default:
				return nil, fmt.Errorf("line %d: unknown option %q", lineNum, key)
			}
		}
		out.Rules = append(out.Rules, rule)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func stripComment(line string) string {
	if idx := strings.Index(line, "#"); idx >= 0 {
		line = line[:idx]
	}
	if idx := strings.Index(line, "//"); idx >= 0 {
		line = line[:idx]
	}
	return line
}

// Resolve evaluates all rules in order against the given candidate names
// (for example, "MyMessage.my_field" and "pkg.MyMessage.my_field").
// Later matching rules override earlier ones.
func (f *File) Resolve(candidates ...string) Options {
	var res Options
	if f == nil {
		return res
	}
	for _, rule := range f.Rules {
		for _, cand := range candidates {
			if cand != "" && Match(rule.Pattern, cand) {
				res = Merge(res, rule.Opts)
				break
			}
		}
	}
	return res
}

// DiscoverSidecar looks for a .options file matching protoPath in the given search directories.
// For "foo/bar.proto", it checks "<dir>/foo/bar.options" and "<dir>/bar.options".
func DiscoverSidecar(protoPath string, searchDirs []string) (*File, error) {
	ext := filepath.Ext(protoPath)
	relOpt := strings.TrimSuffix(protoPath, ext) + ".options"
	baseOpt := filepath.Base(relOpt)

	seen := make(map[string]bool)
	tryPaths := []string{relOpt}
	for _, dir := range searchDirs {
		if dir == "" {
			continue
		}
		tryPaths = append(tryPaths, filepath.Join(dir, relOpt), filepath.Join(dir, baseOpt))
	}

	for _, p := range tryPaths {
		clean := filepath.Clean(p)
		if seen[clean] {
			continue
		}
		seen[clean] = true
		if info, err := os.Stat(clean); err == nil && !info.IsDir() {
			return ParseFile(clean)
		}
	}
	return nil, nil
}

// Match performs fnmatch-style wildcard matching where '*' matches any sequence
// of characters (including '.') and '?' matches any single character.
func Match(pattern, s string) bool {
	pi, si := 0, 0
	starIdx, matchIdx := -1, 0

	for si < len(s) {
		if pi < len(pattern) && (pattern[pi] == '?' || pattern[pi] == s[si]) {
			pi++
			si++
		} else if pi < len(pattern) && pattern[pi] == '*' {
			starIdx = pi
			matchIdx = si
			pi++
		} else if starIdx != -1 {
			pi = starIdx + 1
			matchIdx++
			si = matchIdx
		} else {
			return false
		}
	}

	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern)
}
