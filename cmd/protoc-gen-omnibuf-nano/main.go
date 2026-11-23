package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/bufbuild/protoplugin"
	"github.com/omnibuf/omnibuf-nano/internal/codegen"
	"github.com/omnibuf/omnibuf-nano/internal/options"
)

func main() {
	protoplugin.Main(protoplugin.HandlerFunc(handle))
}

func handle(
	_ context.Context,
	_ protoplugin.PluginEnv,
	responseWriter protoplugin.ResponseWriter,
	request protoplugin.Request,
) error {
	responseWriter.SetFeatureProto3Optional()

	var explicitOptionsFile string
	var searchDirs []string

	param := strings.TrimSpace(request.Parameter())
	if param != "" {
		for _, part := range strings.Split(param, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			k, v, ok := strings.Cut(part, "=")
			if !ok {
				return fmt.Errorf("invalid plugin parameter %q (expected key=value)", part)
			}
			switch k {
			case "options_file":
				explicitOptionsFile = v
			case "options_path":
				searchDirs = append(searchDirs, v)
			default:
				return fmt.Errorf("unknown plugin parameter %q", k)
			}
		}
	}

	var globalSidecar *options.File
	if explicitOptionsFile != "" {
		var err error
		globalSidecar, err = options.ParseFile(explicitOptionsFile)
		if err != nil {
			return fmt.Errorf("parse options_file: %w", err)
		}
	}

	files, err := request.FileDescriptorsToGenerate()
	if err != nil {
		return err
	}

	for _, fd := range files {
		sidecar := globalSidecar
		if sidecar == nil {
			discovered, err := options.DiscoverSidecar(fd.Path(), searchDirs)
			if err != nil {
				return err
			}
			sidecar = discovered
		}

		generated, err := codegen.Generate(fd, codegen.Config{Sidecar: sidecar})
		if err != nil {
			return err
		}
		for _, gf := range generated {
			responseWriter.AddFile(gf.Path, gf.Content)
		}
	}

	return nil
}
