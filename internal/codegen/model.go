package codegen

import (
	"fmt"
	"slices"
	"strings"

	"github.com/omnibuf/omnibuf-nano/internal/options"
	"google.golang.org/protobuf/reflect/protoreflect"
)

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
			wt, err := wireTypeMacro(fd)
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
			wt, err := wireTypeMacro(fd)
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
			} else if isMessageField(rf.desc) {
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

func wireTypeMacro(fd protoreflect.FieldDescriptor) (string, error) {
	if isMessageField(fd) {
		return "OB_WIRE_SUBMSG", nil
	}
	switch fd.Kind() {
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
	default:
		return "", fmt.Errorf("unsupported protobuf field kind %v", fd.Kind())
	}
}

func cTypeName(full protoreflect.FullName) string {
	return strings.ReplaceAll(string(full), ".", "_")
}

var cKeywords = map[string]bool{
	// C89 / C99 / C11 / C23 keywords
	"auto": true, "break": true, "case": true, "char": true, "const": true,
	"continue": true, "default": true, "do": true, "double": true, "else": true,
	"enum": true, "extern": true, "float": true, "for": true, "goto": true,
	"if": true, "inline": true, "int": true, "long": true, "register": true,
	"restrict": true, "return": true, "short": true, "signed": true, "sizeof": true,
	"static": true, "struct": true, "switch": true, "typedef": true, "union": true,
	"unsigned": true, "void": true, "volatile": true, "while": true,
	"bool": true, "true": true, "false": true, "nullptr": true,
	"_Alignas": true, "_Alignof": true, "_Atomic": true, "_Bool": true,
	"_Complex": true, "_Generic": true, "_Imaginary": true, "_Noreturn": true,
	"_Static_assert": true, "_Thread_local": true,
	// C++ keywords (headers are extern "C" compatible)
	"alignas": true, "alignof": true, "and": true, "and_eq": true, "asm": true,
	"bitand": true, "bitor": true, "catch": true, "char8_t": true, "char16_t": true,
	"char32_t": true, "class": true, "compl": true, "concept": true,
	"const_cast": true, "consteval": true, "constexpr": true, "constinit": true,
	"co_await": true, "co_return": true, "co_yield": true, "decltype": true,
	"delete": true, "dynamic_cast": true, "explicit": true, "export": true,
	"friend": true, "mutable": true, "namespace": true, "new": true,
	"noexcept": true, "not": true, "not_eq": true, "operator": true,
	"or": true, "or_eq": true, "private": true, "protected": true,
	"public": true, "reinterpret_cast": true, "requires": true,
	"static_assert": true, "static_cast": true, "template": true, "this": true,
	"thread_local": true, "throw": true, "try": true, "typeid": true,
	"typename": true, "using": true, "virtual": true, "wchar_t": true,
	"xor": true, "xor_eq": true,
}

func sanitizeIdent(name string) string {
	if cKeywords[name] {
		return name + "_"
	}
	return name
}
