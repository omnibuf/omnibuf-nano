package codegen

import (
	"fmt"
	"math"
	"strings"

	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// isMessageField reports whether fd holds a submessage value in OmniBuf.
// In Protobuf Editions, fields with message_encoding = DELIMITED surface as
// GroupKind with a non-nil Message descriptor and encode as normal OB_WIRE_SUBMSG.
// Legacy proto2 groups remain rejected.
func isMessageField(fd protoreflect.FieldDescriptor) bool {
	if fd.Kind() == protoreflect.MessageKind {
		return true
	}
	return fd.Kind() == protoreflect.GroupKind &&
		fd.ParentFile().Syntax() == protoreflect.Editions &&
		fd.Message() != nil
}

func validateEditionsFile(fd protoreflect.FileDescriptor) error {
	if fd.Syntax() != protoreflect.Editions {
		return nil
	}

	ed := fileEdition(fd)
	switch ed {
	case descriptorpb.Edition_EDITION_2023,
		descriptorpb.Edition_EDITION_2024,
		descriptorpb.Edition_EDITION_2026:
	default:
		return fmt.Errorf("%s: unsupported protobuf edition %v (supported: EDITION_2023, EDITION_2024, EDITION_2026)", fd.Path(), ed)
	}

	exts := fd.Extensions()
	for i := 0; i < exts.Len(); i++ {
		ext := exts.Get(i)
		if !isDescriptorOptionExtension(ext) {
			return fmt.Errorf("%s: extensions are not supported in Editions", ext.FullName())
		}
	}

	enums := fd.Enums()
	for i := 0; i < enums.Len(); i++ {
		enumDesc := enums.Get(i)
		if enumDesc.IsClosed() {
			return fmt.Errorf("%s: CLOSED enums are not supported in Editions", enumDesc.FullName())
		}
	}

	// Editions 2023, 2024, and 2026 all default to utf8_validation = VERIFY.
	fileUTF8 := mergeUTF8Validation(descriptorpb.FeatureSet_VERIFY, fileFeatures(fd))

	msgs := fd.Messages()
	for i := 0; i < msgs.Len(); i++ {
		if err := validateEditionsMessage(msgs.Get(i), fileUTF8); err != nil {
			return err
		}
	}
	return nil
}

func validateEditionsMessage(md protoreflect.MessageDescriptor, parentUTF8 descriptorpb.FeatureSet_Utf8Validation) error {
	if md.ExtensionRanges().Len() > 0 {
		return fmt.Errorf("%s: extension ranges are not supported in Editions", md.FullName())
	}

	exts := md.Extensions()
	for i := 0; i < exts.Len(); i++ {
		ext := exts.Get(i)
		if !isDescriptorOptionExtension(ext) {
			return fmt.Errorf("%s: extensions are not supported in Editions", ext.FullName())
		}
	}

	enums := md.Enums()
	for i := 0; i < enums.Len(); i++ {
		enumDesc := enums.Get(i)
		if enumDesc.IsClosed() {
			return fmt.Errorf("%s: CLOSED enums are not supported in Editions", enumDesc.FullName())
		}
	}

	msgUTF8 := mergeUTF8Validation(parentUTF8, messageFeatures(md))

	fds := md.Fields()
	for i := 0; i < fds.Len(); i++ {
		f := fds.Get(i)
		if f.Cardinality() == protoreflect.Required {
			return fmt.Errorf("%s: field_presence = LEGACY_REQUIRED is not supported in Editions", f.FullName())
		}
		if f.Kind() == protoreflect.EnumKind && f.Enum() != nil && f.Enum().IsClosed() {
			return fmt.Errorf("%s: CLOSED enum %s is not supported in Editions", f.FullName(), f.Enum().FullName())
		}

		fieldUTF8 := msgUTF8
		if od := f.ContainingOneof(); od != nil && !od.IsSynthetic() {
			fieldUTF8 = mergeUTF8Validation(fieldUTF8, oneofFeatures(od))
		}
		fieldUTF8 = mergeUTF8Validation(fieldUTF8, fieldFeatures(f))

		if f.Kind() == protoreflect.StringKind && fieldUTF8 == descriptorpb.FeatureSet_NONE {
			return fmt.Errorf("%s: utf8_validation = NONE is not supported in Editions", f.FullName())
		}
		if f.HasDefault() && !isZeroDefault(f) {
			return fmt.Errorf("%s: non-zero default value is not supported in Editions", f.FullName())
		}

		if f.IsMap() {
			entry := f.Message()
			entryUTF8 := mergeUTF8Validation(fieldUTF8, messageFeatures(entry))
			for _, ef := range []protoreflect.FieldDescriptor{f.MapKey(), f.MapValue()} {
				if ef == nil {
					continue
				}
				if ef.Kind() == protoreflect.EnumKind && ef.Enum() != nil && ef.Enum().IsClosed() {
					return fmt.Errorf("%s: CLOSED enum %s is not supported in Editions", f.FullName(), ef.Enum().FullName())
				}
				efUTF8 := mergeUTF8Validation(entryUTF8, fieldFeatures(ef))
				if ef.Kind() == protoreflect.StringKind && efUTF8 == descriptorpb.FeatureSet_NONE {
					return fmt.Errorf("%s: utf8_validation = NONE is not supported on map %s in Editions", f.FullName(), ef.Name())
				}
			}
		}
	}

	nested := md.Messages()
	for i := 0; i < nested.Len(); i++ {
		child := nested.Get(i)
		if child.IsMapEntry() {
			continue
		}
		if err := validateEditionsMessage(child, msgUTF8); err != nil {
			return err
		}
	}
	return nil
}

func fileEdition(fd protoreflect.FileDescriptor) descriptorpb.Edition {
	if fdp := protodesc.ToFileDescriptorProto(fd); fdp != nil {
		return fdp.GetEdition()
	}
	return descriptorpb.Edition_EDITION_UNKNOWN
}

func isDescriptorOptionExtension(ext protoreflect.ExtensionDescriptor) bool {
	if ext.ContainingMessage() == nil {
		return false
	}
	name := string(ext.ContainingMessage().FullName())
	return strings.HasPrefix(name, "google.protobuf.") && strings.HasSuffix(name, "Options")
}

func fileFeatures(fd protoreflect.FileDescriptor) *descriptorpb.FeatureSet {
	if opts, ok := fd.Options().(*descriptorpb.FileOptions); ok {
		return opts.GetFeatures()
	}
	return nil
}

func messageFeatures(md protoreflect.MessageDescriptor) *descriptorpb.FeatureSet {
	if opts, ok := md.Options().(*descriptorpb.MessageOptions); ok {
		return opts.GetFeatures()
	}
	return nil
}

func oneofFeatures(od protoreflect.OneofDescriptor) *descriptorpb.FeatureSet {
	if opts, ok := od.Options().(*descriptorpb.OneofOptions); ok {
		return opts.GetFeatures()
	}
	return nil
}

func fieldFeatures(fd protoreflect.FieldDescriptor) *descriptorpb.FeatureSet {
	if opts, ok := fd.Options().(*descriptorpb.FieldOptions); ok {
		return opts.GetFeatures()
	}
	return nil
}

func mergeUTF8Validation(
	parent descriptorpb.FeatureSet_Utf8Validation,
	fs *descriptorpb.FeatureSet,
) descriptorpb.FeatureSet_Utf8Validation {
	if fs != nil && fs.Utf8Validation != nil && *fs.Utf8Validation != descriptorpb.FeatureSet_UTF8_VALIDATION_UNKNOWN {
		return *fs.Utf8Validation
	}
	return parent
}

func isZeroDefault(fd protoreflect.FieldDescriptor) bool {
	v := fd.Default()
	switch fd.Kind() {
	case protoreflect.BoolKind:
		return !v.Bool()
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return v.Int() == 0
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind,
		protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return v.Uint() == 0
	case protoreflect.FloatKind:
		return math.Float32bits(float32(v.Float())) == 0
	case protoreflect.DoubleKind:
		return math.Float64bits(v.Float()) == 0
	case protoreflect.StringKind:
		return v.String() == ""
	case protoreflect.BytesKind:
		return len(v.Bytes()) == 0
	case protoreflect.EnumKind:
		return v.Enum() == 0
	default:
		return false
	}
}
