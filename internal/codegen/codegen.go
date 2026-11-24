package codegen

import (
	"bytes"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/omnibuf/omnibuf-nano/internal/options"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const nanoExtensionNumber protowire.Number = 51000

// Config controls code generation for a single .proto file.
type Config struct {
	Sidecar *options.File
}

// GeneratedFile represents one emitted C header or source file.
type GeneratedFile struct {
	Path    string
	Content string
}

// Generate emits <stem>.ob.h and <stem>.ob.c for fd.
func Generate(fd protoreflect.FileDescriptor, cfg Config) ([]GeneratedFile, error) {
	fileDefaults := extractFileOptions(fd)

	g := &fileGenerator{
		fd:           fd,
		sidecar:      cfg.Sidecar,
		fileDefaults: fileDefaults,
	}

	if err := g.collectTypes(); err != nil {
		return nil, err
	}

	stem := strings.TrimSuffix(fd.Path(), filepath.Ext(fd.Path()))
	headerPath := stem + ".ob.h"
	sourcePath := stem + ".ob.c"

	header, err := g.renderHeader(headerPath)
	if err != nil {
		return nil, err
	}
	source, err := g.renderSource(headerPath)
	if err != nil {
		return nil, err
	}

	return []GeneratedFile{
		{Path: headerPath, Content: header},
		{Path: sourcePath, Content: source},
	}, nil
}

type fileGenerator struct {
	fd           protoreflect.FileDescriptor
	sidecar      *options.File
	fileDefaults options.Options

	enums    []protoreflect.EnumDescriptor
	messages []*resolvedMessage
	msgByKey map[protoreflect.FullName]*resolvedMessage
}

type resolvedMessage struct {
	desc     protoreflect.MessageDescriptor
	cName    string
	isMap    bool
	mapField protoreflect.FieldDescriptor // non-nil when isMap == true
	fields   []*resolvedField
	oneofs   []*resolvedOneof
}

type resolvedOneof struct {
	desc   protoreflect.OneofDescriptor
	cName  string
	fields []*resolvedField
}

type resolvedField struct {
	desc     protoreflect.FieldDescriptor
	cName    string
	tag      uint16
	wireType string
	flags    []string
	opts     options.Options
	oneof    *resolvedOneof
	mapEntry *resolvedMessage
}

func (g *fileGenerator) collectTypes() error {
	g.msgByKey = make(map[protoreflect.FullName]*resolvedMessage)

	// Collect top-level enums.
	enums := g.fd.Enums()
	for i := 0; i < enums.Len(); i++ {
		g.enums = append(g.enums, enums.Get(i))
	}

	// Recursively collect messages (and nested enums / map entries).
	var rawMsgs []*resolvedMessage
	var walk func(md protoreflect.MessageDescriptor) error
	walk = func(md protoreflect.MessageDescriptor) error {
		if md.IsMapEntry() {
			return nil
		}
		mopts := g.resolveMessageOptions(md)
		if mopts.SkipMessage {
			return nil
		}

		nestedEnums := md.Enums()
		for i := 0; i < nestedEnums.Len(); i++ {
			g.enums = append(g.enums, nestedEnums.Get(i))
		}

		rm, err := g.buildMessage(md, nil)
		if err != nil {
			return err
		}
		rawMsgs = append(rawMsgs, rm)
		g.msgByKey[md.FullName()] = rm

		// Build synthetic map entry structs for map fields on this message.
		for _, rf := range rm.fields {
			if rf.mapEntry != nil {
				rawMsgs = append(rawMsgs, rf.mapEntry)
				g.msgByKey[rf.mapEntry.desc.FullName()] = rf.mapEntry
			}
		}

		nested := md.Messages()
		for i := 0; i < nested.Len(); i++ {
			if err := walk(nested.Get(i)); err != nil {
				return err
			}
		}
		return nil
	}

	msgs := g.fd.Messages()
	for i := 0; i < msgs.Len(); i++ {
		if err := walk(msgs.Get(i)); err != nil {
			return err
		}
	}

	sorted, err := g.topoSort(rawMsgs)
	if err != nil {
		return err
	}
	g.messages = sorted
	return nil
}

func (g *fileGenerator) buildMessage(md protoreflect.MessageDescriptor, mapParentField protoreflect.FieldDescriptor) (*resolvedMessage, error) {
	rm := &resolvedMessage{
		desc:     md,
		cName:    cTypeName(md.FullName()),
		isMap:    mapParentField != nil,
		mapField: mapParentField,
	}

	oneofMap := make(map[protoreflect.FullName]*resolvedOneof)
	ods := md.Oneofs()
	for i := 0; i < ods.Len(); i++ {
		od := ods.Get(i)
		if od.IsSynthetic() {
			continue
		}
		ro := &resolvedOneof{
			desc:  od,
			cName: sanitizeIdent(string(od.Name())),
		}
		rm.oneofs = append(rm.oneofs, ro)
		oneofMap[od.FullName()] = ro
	}

	fds := md.Fields()
	for i := 0; i < fds.Len(); i++ {
		fd := fds.Get(i)
		if fd.Number() <= 0 || fd.Number() > 65535 {
			return nil, fmt.Errorf("%s: field number %d out of uint16 range", fd.FullName(), fd.Number())
		}

		var fopts options.Options
		if mapParentField != nil {
			fopts = g.resolveMapEntryFieldOptions(mapParentField, fd)
		} else {
			fopts = g.resolveFieldOptions(fd)
		}

		if fopts.MaxCount > 65535 {
			return nil, fmt.Errorf("%s: max_count %d exceeds uint16 limit (65535)", fd.FullName(), fopts.MaxCount)
		}
		if fopts.MaxSize > 65535 || (fd.Kind() == protoreflect.BytesKind && !fopts.FixedLength && fopts.MaxSize > 65533) {
			return nil, fmt.Errorf("%s: max_size %d exceeds uint16 descriptor limit", fd.FullName(), fopts.MaxSize)
		}

		rf := &resolvedField{
			desc:  fd,
			cName: sanitizeIdent(string(fd.Name())),
			tag:   uint16(fd.Number()),
			opts:  fopts,
		}

		if od := fd.ContainingOneof(); od != nil && !od.IsSynthetic() {
			ro := oneofMap[od.FullName()]
			rf.oneof = ro
			ro.fields = append(ro.fields, rf)
		}

		if fd.IsMap() {
			if fopts.MaxCount == 0 {
				return nil, fmt.Errorf("%s: map field requires max_count option", fd.FullName())
			}
			rf.wireType = "OB_WIRE_MAP"
			rf.flags = []string{"OB_FLAG_MAP"}
			entryMsg, err := g.buildMessage(fd.Message(), fd)
			if err != nil {
				return nil, err
			}
			rf.mapEntry = entryMsg
		} else if fd.IsList() {
			if fopts.MaxCount == 0 {
				return nil, fmt.Errorf("%s: repeated field requires max_count option", fd.FullName())
			}
			wt, err := wireTypeMacro(fd.Kind())
			if err != nil {
				return nil, fmt.Errorf("%s: %w", fd.FullName(), err)
			}
			rf.wireType = wt
			rf.flags = []string{"OB_FLAG_REPEATED"}
			if fopts.FixedLength {
				rf.flags = append(rf.flags, "OB_FLAG_FIXED_LEN")
			}
			if (fd.Kind() == protoreflect.StringKind || fd.Kind() == protoreflect.BytesKind) && fopts.MaxSize == 0 {
				return nil, fmt.Errorf("%s: repeated %s field requires max_size option", fd.FullName(), fd.Kind())
			}
		} else {
			wt, err := wireTypeMacro(fd.Kind())
			if err != nil {
				return nil, fmt.Errorf("%s: %w", fd.FullName(), err)
			}
			rf.wireType = wt
			if (fd.Kind() == protoreflect.StringKind || fd.Kind() == protoreflect.BytesKind) && fopts.MaxSize == 0 {
				return nil, fmt.Errorf("%s: %s field requires max_size option", fd.FullName(), fd.Kind())
			}
			switch {
			case rf.oneof != nil:
				rf.flags = []string{"OB_FLAG_ONEOF"}
			case fd.Cardinality() == protoreflect.Required:
				rf.flags = []string{"OB_FLAG_REQUIRED"}
			case fd.HasPresence() && mapParentField == nil:
				rf.flags = []string{"OB_FLAG_OPTIONAL"}
			default:
				rf.flags = []string{"OB_FLAG_SINGULAR"}
			}
			if fd.Kind() == protoreflect.BytesKind && fopts.FixedLength {
				rf.flags = append(rf.flags, "OB_FLAG_FIXED_LEN")
			}
		}

		rm.fields = append(rm.fields, rf)
	}

	// Sort descriptor fields by tag ascending for binary search in ob_decode.
	slices.SortFunc(rm.fields, func(a, b *resolvedField) int {
		if a.tag < b.tag {
			return -1
		}
		if a.tag > b.tag {
			return 1
		}
		return 0
	})

	for i, rf := range rm.fields {
		if i >= 256 && slices.Contains(rf.flags, "OB_FLAG_REQUIRED") {
			return nil, fmt.Errorf("%s: required field at descriptor index %d exceeds runtime limit (255)", rf.desc.FullName(), i)
		}
	}

	return rm, nil
}

func (g *fileGenerator) topoSort(msgs []*resolvedMessage) ([]*resolvedMessage, error) {
	const (
		unvisited = 0
		visiting  = 1
		visited   = 2
	)
	state := make(map[protoreflect.FullName]int, len(msgs))
	var out []*resolvedMessage

	var visit func(m *resolvedMessage) error
	visit = func(m *resolvedMessage) error {
		key := m.desc.FullName()
		switch state[key] {
		case visited:
			return nil
		case visiting:
			return fmt.Errorf("cyclic value dependency involving message %s (not supported in static C structs)", key)
		}
		state[key] = visiting

		for _, rf := range m.fields {
			var dep *resolvedMessage
			if rf.mapEntry != nil {
				dep = rf.mapEntry
			} else if rf.desc.Kind() == protoreflect.MessageKind {
				if target, ok := g.msgByKey[rf.desc.Message().FullName()]; ok {
					dep = target
				}
			}
			if dep != nil {
				if err := visit(dep); err != nil {
					return err
				}
			}
		}

		state[key] = visited
		out = append(out, m)
		return nil
	}

	for _, m := range msgs {
		if err := visit(m); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (g *fileGenerator) renderHeader(headerPath string) (string, error) {
	var b bytes.Buffer
	guard := includeGuard(headerPath)

	fmt.Fprintf(&b, "/* Automatically generated by protoc-gen-omnibuf-nano. DO NOT EDIT. */\n")
	fmt.Fprintf(&b, "/* Source: %s */\n\n", g.fd.Path())
	fmt.Fprintf(&b, "#ifndef %s\n", guard)
	fmt.Fprintf(&b, "#define %s\n\n", guard)
	fmt.Fprintf(&b, "#include \"ob_common.h\"\n")

	imports := g.fd.Imports()
	for i := 0; i < imports.Len(); i++ {
		imp := imports.Get(i)
		impPath := imp.Path()
		if impPath == "omnibuf/nano/v1/options.proto" || strings.HasPrefix(impPath, "google/protobuf/") {
			continue
		}
		impHeader := strings.TrimSuffix(impPath, filepath.Ext(impPath)) + ".ob.h"
		fmt.Fprintf(&b, "#include \"%s\"\n", impHeader)
	}

	fmt.Fprintf(&b, "\n#ifdef __cplusplus\nextern \"C\" {\n#endif\n\n")

	// Enums (backed by int32_t so -fshort-enums cannot narrow them to unsigned 1/2-byte types)
	for _, ed := range g.enums {
		eName := cTypeName(ed.FullName())
		fmt.Fprintf(&b, "typedef int32_t %s;\n", eName)
		fmt.Fprintf(&b, "enum {\n")
		vals := ed.Values()
		for i := 0; i < vals.Len(); i++ {
			ev := vals.Get(i)
			comma := ","
			if i == vals.Len()-1 {
				comma = ""
			}
			fmt.Fprintf(&b, "    %s_%s = %d%s\n", eName, ev.Name(), ev.Number(), comma)
		}
		fmt.Fprintf(&b, "};\n\n")
	}

	// Message structs
	for _, rm := range g.messages {
		fmt.Fprintf(&b, "typedef struct {\n")
		emittedOneofs := make(map[protoreflect.FullName]bool)
		fieldCount := 0

		for _, rf := range rm.fields {
			if rf.oneof != nil {
				if emittedOneofs[rf.oneof.desc.FullName()] {
					continue
				}
				emittedOneofs[rf.oneof.desc.FullName()] = true
				fmt.Fprintf(&b, "    uint16_t which_%s;\n", rf.oneof.cName)
				fmt.Fprintf(&b, "    union {\n")
				for _, arm := range rf.oneof.fields {
					fmt.Fprintf(&b, "        %s;\n", fieldDecl(arm))
				}
				fmt.Fprintf(&b, "    } %s;\n", rf.oneof.cName)
				fieldCount++
				continue
			}

			if rf.desc.IsMap() {
				fmt.Fprintf(&b, "    uint16_t %s_count;\n", rf.cName)
				fmt.Fprintf(&b, "    %s %s[%du];\n", rf.mapEntry.cName, rf.cName, rf.opts.MaxCount)
				fieldCount++
				continue
			}

			if rf.desc.IsList() {
				if !rf.opts.FixedLength {
					fmt.Fprintf(&b, "    uint16_t %s_count;\n", rf.cName)
				}
				fmt.Fprintf(&b, "    %s;\n", repeatedFieldDecl(rf))
				fieldCount++
				continue
			}

			if slices.Contains(rf.flags, "OB_FLAG_OPTIONAL") {
				fmt.Fprintf(&b, "    bool has_%s;\n", rf.cName)
			}
			fmt.Fprintf(&b, "    %s;\n", fieldDecl(rf))
			fieldCount++
		}

		if fieldCount == 0 {
			/* C99 forbids empty structs. */
			fmt.Fprintf(&b, "    uint8_t _dummy;\n")
		}
		fmt.Fprintf(&b, "} %s;\n\n", rm.cName)

		// Tag constants
		for _, rf := range rm.fields {
			fmt.Fprintf(&b, "#define %s_%s_tag %du\n", rm.cName, rf.cName, rf.tag)
		}
		fmt.Fprintf(&b, "#define %s_init_zero {0}\n", rm.cName)
		fmt.Fprintf(&b, "extern const ob_msgdesc_t %s_msg;\n\n", rm.cName)
	}

	fmt.Fprintf(&b, "#ifdef __cplusplus\n}\n#endif\n\n")
	fmt.Fprintf(&b, "#endif /* %s */\n", guard)
	return b.String(), nil
}

func (g *fileGenerator) renderSource(headerPath string) (string, error) {
	var b bytes.Buffer
	fmt.Fprintf(&b, "/* Automatically generated by protoc-gen-omnibuf-nano. DO NOT EDIT. */\n")
	fmt.Fprintf(&b, "/* Source: %s */\n\n", g.fd.Path())
	fmt.Fprintf(&b, "#include \"%s\"\n\n", filepath.Base(headerPath))

	for _, rm := range g.messages {
		fmt.Fprintf(&b, "typedef char _ob_check_%s_size[(sizeof(%s) <= 65535u) ? 1 : -1];\n", rm.cName, rm.cName)
		if len(rm.fields) > 0 {
			fmt.Fprintf(&b, "static const ob_field_t %s_fields[%d] = {\n", rm.cName, len(rm.fields))
			for i, rf := range rm.fields {
				comma := ","
				if i == len(rm.fields)-1 {
					comma = ""
				}
				dataOff := fmt.Sprintf("offsetof(%s, %s)", rm.cName, rf.cName)
				sizeOff := "0u"
				dataSize := fieldDataSizeExpr(rm.cName, rf)
				arraySize := "1u"
				submsg := "NULL"

				if rf.oneof != nil {
					dataOff = fmt.Sprintf("offsetof(%s, %s.%s)", rm.cName, rf.oneof.cName, rf.cName)
					sizeOff = fmt.Sprintf("offsetof(%s, which_%s)", rm.cName, rf.oneof.cName)
				} else if rf.desc.IsMap() {
					sizeOff = fmt.Sprintf("offsetof(%s, %s_count)", rm.cName, rf.cName)
					dataSize = fmt.Sprintf("sizeof(%s)", rf.mapEntry.cName)
					arraySize = fmt.Sprintf("%du", rf.opts.MaxCount)
					submsg = fmt.Sprintf("&%s_msg", rf.mapEntry.cName)
				} else if rf.desc.IsList() {
					if !rf.opts.FixedLength {
						sizeOff = fmt.Sprintf("offsetof(%s, %s_count)", rm.cName, rf.cName)
					}
					arraySize = fmt.Sprintf("%du", rf.opts.MaxCount)
				} else if slices.Contains(rf.flags, "OB_FLAG_OPTIONAL") {
					sizeOff = fmt.Sprintf("offsetof(%s, has_%s)", rm.cName, rf.cName)
				}

				if !rf.desc.IsMap() && rf.desc.Kind() == protoreflect.MessageKind {
					submsg = fmt.Sprintf("&%s_msg", cTypeName(rf.desc.Message().FullName()))
				}

				fmt.Fprintf(&b, "    {%du, %s, %s, (uint16_t)%s, (uint16_t)%s, (uint16_t)%s, %s, %s}%s\n",
					rf.tag,
					rf.wireType,
					strings.Join(rf.flags, " | "),
					dataOff,
					sizeOff,
					dataSize,
					arraySize,
					submsg,
					comma,
				)
			}
			fmt.Fprintf(&b, "};\n\n")
			fmt.Fprintf(&b, "const ob_msgdesc_t %s_msg = {\n", rm.cName)
			fmt.Fprintf(&b, "    %s_fields,\n", rm.cName)
			fmt.Fprintf(&b, "    %du,\n", len(rm.fields))
			fmt.Fprintf(&b, "    (uint16_t)sizeof(%s)\n", rm.cName)
			fmt.Fprintf(&b, "};\n\n")
		} else {
			fmt.Fprintf(&b, "const ob_msgdesc_t %s_msg = {\n", rm.cName)
			fmt.Fprintf(&b, "    NULL,\n")
			fmt.Fprintf(&b, "    0u,\n")
			fmt.Fprintf(&b, "    (uint16_t)sizeof(%s)\n", rm.cName)
			fmt.Fprintf(&b, "};\n\n")
		}
	}
	return b.String(), nil
}

func fieldDecl(rf *resolvedField) string {
	switch rf.desc.Kind() {
	case protoreflect.StringKind:
		return fmt.Sprintf("char %s[%du]", rf.cName, rf.opts.MaxSize)
	case protoreflect.BytesKind:
		if rf.opts.FixedLength {
			return fmt.Sprintf("uint8_t %s[%du]", rf.cName, rf.opts.MaxSize)
		}
		return fmt.Sprintf("OB_BYTES_ARRAY_T(%du) %s", rf.opts.MaxSize, rf.cName)
	case protoreflect.MessageKind:
		return fmt.Sprintf("%s %s", cTypeName(rf.desc.Message().FullName()), rf.cName)
	case protoreflect.EnumKind:
		return fmt.Sprintf("%s %s", cTypeName(rf.desc.Enum().FullName()), rf.cName)
	default:
		return fmt.Sprintf("%s %s", scalarCType(rf.desc.Kind()), rf.cName)
	}
}

func repeatedFieldDecl(rf *resolvedField) string {
	switch rf.desc.Kind() {
	case protoreflect.StringKind:
		return fmt.Sprintf("char %s[%du][%du]", rf.cName, rf.opts.MaxCount, rf.opts.MaxSize)
	case protoreflect.BytesKind:
		if rf.opts.FixedLength {
			return fmt.Sprintf("uint8_t %s[%du][%du]", rf.cName, rf.opts.MaxCount, rf.opts.MaxSize)
		}
		return fmt.Sprintf("OB_BYTES_ARRAY_T(%du) %s[%du]", rf.opts.MaxSize, rf.cName, rf.opts.MaxCount)
	case protoreflect.MessageKind:
		return fmt.Sprintf("%s %s[%du]", cTypeName(rf.desc.Message().FullName()), rf.cName, rf.opts.MaxCount)
	case protoreflect.EnumKind:
		return fmt.Sprintf("%s %s[%du]", cTypeName(rf.desc.Enum().FullName()), rf.cName, rf.opts.MaxCount)
	default:
		return fmt.Sprintf("%s %s[%du]", scalarCType(rf.desc.Kind()), rf.cName, rf.opts.MaxCount)
	}
}

func fieldDataSizeExpr(msgCName string, rf *resolvedField) string {
	switch rf.desc.Kind() {
	case protoreflect.StringKind, protoreflect.BytesKind:
		return fmt.Sprintf("%du", rf.opts.MaxSize)
	case protoreflect.MessageKind:
		return fmt.Sprintf("sizeof(%s)", cTypeName(rf.desc.Message().FullName()))
	case protoreflect.EnumKind:
		return fmt.Sprintf("sizeof(%s)", cTypeName(rf.desc.Enum().FullName()))
	default:
		_ = msgCName
		return fmt.Sprintf("sizeof(%s)", scalarCType(rf.desc.Kind()))
	}
}

func scalarCType(k protoreflect.Kind) string {
	switch k {
	case protoreflect.BoolKind:
		return "bool"
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		return "int32_t"
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return "int64_t"
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return "uint32_t"
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return "uint64_t"
	case protoreflect.FloatKind:
		return "float"
	case protoreflect.DoubleKind:
		return "double"
	default:
		return "uint32_t"
	}
}

func wireTypeMacro(k protoreflect.Kind) (string, error) {
	switch k {
	case protoreflect.Uint32Kind, protoreflect.Uint64Kind:
		return "OB_WIRE_UINT", nil
	case protoreflect.EnumKind, protoreflect.Int32Kind, protoreflect.Sint32Kind,
		protoreflect.Int64Kind, protoreflect.Sint64Kind:
		return "OB_WIRE_SINT", nil
	case protoreflect.BoolKind:
		return "OB_WIRE_BOOL_FALSE", nil
	case protoreflect.Fixed32Kind:
		return "OB_WIRE_FIXED32", nil
	case protoreflect.Fixed64Kind:
		return "OB_WIRE_FIXED64", nil
	case protoreflect.Sfixed32Kind:
		return "OB_WIRE_SFIXED32", nil
	case protoreflect.Sfixed64Kind:
		return "OB_WIRE_SFIXED64", nil
	case protoreflect.FloatKind:
		return "OB_WIRE_FLOAT32", nil
	case protoreflect.DoubleKind:
		return "OB_WIRE_FLOAT64", nil
	case protoreflect.StringKind:
		return "OB_WIRE_STRING", nil
	case protoreflect.BytesKind:
		return "OB_WIRE_BYTES", nil
	case protoreflect.MessageKind:
		return "OB_WIRE_SUBMSG", nil
	default:
		return "", fmt.Errorf("unsupported protobuf field kind %v", k)
	}
}

func cTypeName(full protoreflect.FullName) string {
	return strings.ReplaceAll(string(full), ".", "_")
}

func includeGuard(path string) string {
	var b strings.Builder
	b.WriteString("OMNIBUF_NANO_")
	for _, r := range path {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 'a' + 'A')
		case (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

var cKeywords = map[string]bool{
	"auto": true, "break": true, "case": true, "char": true, "const": true,
	"continue": true, "default": true, "do": true, "double": true, "else": true,
	"enum": true, "extern": true, "float": true, "for": true, "goto": true,
	"if": true, "inline": true, "int": true, "long": true, "register": true,
	"restrict": true, "return": true, "short": true, "signed": true, "sizeof": true,
	"static": true, "struct": true, "switch": true, "typedef": true, "union": true,
	"unsigned": true, "void": true, "volatile": true, "while": true,
}

func sanitizeIdent(name string) string {
	if cKeywords[name] {
		return name + "_"
	}
	return name
}

// ---------- Option Resolution & Extension Extraction ----------

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
