package codegen

import (
	"path/filepath"
	"strings"

	"github.com/omnibuf/omnibuf-nano/internal/options"
	"google.golang.org/protobuf/reflect/protoreflect"
)

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
