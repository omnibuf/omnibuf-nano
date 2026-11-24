package codegen

import (
	"strings"

	"github.com/omnibuf/omnibuf-nano/internal/options"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const nanoExtensionNumber protowire.Number = 51000

func (g *fileGenerator) resolveMessageOptions(md protoreflect.MessageDescriptor) options.Options {
	protoOpts := extractMessageOptions(md)
	if g.sidecar == nil {
		return protoOpts
	}
	cands := messageCandidates(md)
	sideOpts := g.sidecar.Resolve(cands...)
	return options.Merge(protoOpts, sideOpts)
}

func (g *fileGenerator) resolveFieldOptions(fd protoreflect.FieldDescriptor) options.Options {
	base := g.fileDefaults
	protoOpts := extractFieldOptions(fd)
	merged := options.Merge(base, protoOpts)
	if g.sidecar == nil {
		return merged
	}
	cands := fieldCandidates(fd)
	sideOpts := g.sidecar.Resolve(cands...)
	return options.Merge(merged, sideOpts)
}

func (g *fileGenerator) resolveMapEntryFieldOptions(mapField, entryField protoreflect.FieldDescriptor) options.Options {
	// Precedence (lowest to highest):
	// 1. file defaults
	// 2. map field proto options (e.g., max_size on map field applies to string/bytes key/value)
	// 3. map field sidecar options
	// 4. specific entry field sidecar options (e.g., MyMsg.kv.key max_size:16)
	base := g.resolveFieldOptions(mapField)
	if g.sidecar == nil {
		return base
	}
	var cands []string
	for _, parentCand := range fieldCandidates(mapField) {
		cands = append(cands, parentCand+"."+string(entryField.Name()))
	}
	sideOpts := g.sidecar.Resolve(cands...)
	return options.Merge(base, sideOpts)
}

func messageCandidates(md protoreflect.MessageDescriptor) []string {
	full := string(md.FullName())
	pkg := string(md.ParentFile().Package())
	short := full
	if pkg != "" && strings.HasPrefix(full, pkg+".") {
		short = strings.TrimPrefix(full, pkg+".")
	}
	if short == full {
		return []string{full}
	}
	return []string{short, full}
}

func fieldCandidates(fd protoreflect.FieldDescriptor) []string {
	full := string(fd.FullName())
	pkg := string(fd.ParentFile().Package())
	short := full
	if pkg != "" && strings.HasPrefix(full, pkg+".") {
		short = strings.TrimPrefix(full, pkg+".")
	}
	if short == full {
		return []string{full}
	}
	return []string{short, full}
}

func extractRawExtensionBytes(opts protoreflect.ProtoMessage) []byte {
	if opts == nil {
		return nil
	}
	ref := opts.ProtoReflect()
	// Check unknown fields first (standard when options.proto isn't linked into the plugin).
	raw := ref.GetUnknown()
	for len(raw) > 0 {
		num, typ, n := protowire.ConsumeTag(raw)
		if n < 0 {
			break
		}
		raw = raw[n:]
		if num == nanoExtensionNumber && typ == protowire.BytesType {
			val, vn := protowire.ConsumeBytes(raw)
			if vn >= 0 {
				return val
			}
			break
		}
		vn := protowire.ConsumeFieldValue(num, typ, raw)
		if vn < 0 {
			break
		}
		raw = raw[vn:]
	}
	return nil
}

func extractFileOptions(fd protoreflect.FileDescriptor) options.Options {
	var out options.Options
	opts := fd.Options()
	if opts == nil {
		return out
	}
	// Check dynamic/known extension first.
	opts.ProtoReflect().Range(func(xfd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if xfd.Number() == nanoExtensionNumber && xfd.Kind() == protoreflect.MessageKind {
			m := v.Message()
			m.Range(func(f protoreflect.FieldDescriptor, fv protoreflect.Value) bool {
				switch f.Number() {
				case 1:
					out.MaxSize = uint32(fv.Uint())
					out.HasMaxSize = true
				case 2:
					out.MaxCount = uint32(fv.Uint())
					out.HasMaxCount = true
				}
				return true
			})
			return false
		}
		return true
	})
	if raw := extractRawExtensionBytes(opts); len(raw) > 0 {
		for len(raw) > 0 {
			num, typ, n := protowire.ConsumeTag(raw)
			if n < 0 {
				break
			}
			raw = raw[n:]
			if typ == protowire.VarintType {
				v, vn := protowire.ConsumeVarint(raw)
				if vn < 0 {
					break
				}
				raw = raw[vn:]
				switch num {
				case 1:
					out.MaxSize = uint32(v)
					out.HasMaxSize = true
				case 2:
					out.MaxCount = uint32(v)
					out.HasMaxCount = true
				}
			} else {
				vn := protowire.ConsumeFieldValue(num, typ, raw)
				if vn < 0 {
					break
				}
				raw = raw[vn:]
			}
		}
	}
	return out
}

func extractMessageOptions(md protoreflect.MessageDescriptor) options.Options {
	var out options.Options
	opts := md.Options()
	if opts == nil {
		return out
	}
	opts.ProtoReflect().Range(func(xfd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if xfd.Number() == nanoExtensionNumber && xfd.Kind() == protoreflect.MessageKind {
			m := v.Message()
			m.Range(func(f protoreflect.FieldDescriptor, fv protoreflect.Value) bool {
				if f.Number() == 1 {
					out.SkipMessage = fv.Bool()
					out.HasSkipMessage = true
				}
				return true
			})
			return false
		}
		return true
	})
	if raw := extractRawExtensionBytes(opts); len(raw) > 0 {
		for len(raw) > 0 {
			num, typ, n := protowire.ConsumeTag(raw)
			if n < 0 {
				break
			}
			raw = raw[n:]
			if num == 1 && typ == protowire.VarintType {
				v, vn := protowire.ConsumeVarint(raw)
				if vn < 0 {
					break
				}
				raw = raw[vn:]
				out.SkipMessage = v != 0
				out.HasSkipMessage = true
			} else {
				vn := protowire.ConsumeFieldValue(num, typ, raw)
				if vn < 0 {
					break
				}
				raw = raw[vn:]
			}
		}
	}
	return out
}

func extractFieldOptions(fd protoreflect.FieldDescriptor) options.Options {
	var out options.Options
	opts := fd.Options()
	if opts == nil {
		return out
	}
	opts.ProtoReflect().Range(func(xfd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if xfd.Number() == nanoExtensionNumber && xfd.Kind() == protoreflect.MessageKind {
			m := v.Message()
			m.Range(func(f protoreflect.FieldDescriptor, fv protoreflect.Value) bool {
				switch f.Number() {
				case 1:
					out.MaxSize = uint32(fv.Uint())
					out.HasMaxSize = true
				case 2:
					out.MaxCount = uint32(fv.Uint())
					out.HasMaxCount = true
				case 3:
					out.FixedLength = fv.Bool()
					out.HasFixedLength = true
				}
				return true
			})
			return false
		}
		return true
	})
	if raw := extractRawExtensionBytes(opts); len(raw) > 0 {
		for len(raw) > 0 {
			num, typ, n := protowire.ConsumeTag(raw)
			if n < 0 {
				break
			}
			raw = raw[n:]
			if typ == protowire.VarintType {
				v, vn := protowire.ConsumeVarint(raw)
				if vn < 0 {
					break
				}
				raw = raw[vn:]
				switch num {
				case 1:
					out.MaxSize = uint32(v)
					out.HasMaxSize = true
				case 2:
					out.MaxCount = uint32(v)
					out.HasMaxCount = true
				case 3:
					out.FixedLength = v != 0
					out.HasFixedLength = true
				}
			} else {
				vn := protowire.ConsumeFieldValue(num, typ, raw)
				if vn < 0 {
					break
				}
				raw = raw[vn:]
			}
		}
	}
	return out
}
