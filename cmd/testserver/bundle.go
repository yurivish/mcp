package main

import (
	"fmt"
	"io/fs"

	"github.com/evanw/esbuild/pkg/api"
)

// bundleEntryPoints re-bundles the given entry points from srcFS as an IIFE
// with the specified global name. This converts ESM exports into properties
// of a global object (e.g. McpApps.App), making them usable from a regular
// <script> tag that document.write() will execute.
func bundleEntryPoints(srcFS fs.FS, entryPoints []string, globalName string) (string, error) {
	result := api.Build(api.BuildOptions{
		EntryPoints: entryPoints,
		Bundle:      true,
		Format:      api.FormatIIFE,
		GlobalName:  globalName,
		MinifySyntax:      true,
		MinifyWhitespace:  true,
		MinifyIdentifiers: true,
		Write:  false,
		Plugins: []api.Plugin{{
			Name: "fs-loader",
			Setup: func(build api.PluginBuild) {
				build.OnResolve(api.OnResolveOptions{Filter: ".*"}, func(args api.OnResolveArgs) (api.OnResolveResult, error) {
					return api.OnResolveResult{
						Path:      args.Path,
						Namespace: "fs",
					}, nil
				})
				build.OnLoad(api.OnLoadOptions{Filter: ".*", Namespace: "fs"}, func(args api.OnLoadArgs) (api.OnLoadResult, error) {
					data, err := fs.ReadFile(srcFS, args.Path)
					if err != nil {
						return api.OnLoadResult{}, fmt.Errorf("reading %s: %w", args.Path, err)
					}
					contents := string(data)
					return api.OnLoadResult{
						Contents: &contents,
						Loader:   api.LoaderJS,
					}, nil
				})
			},
		}},
	})

	if len(result.Errors) > 0 {
		msgs := api.FormatMessages(result.Errors, api.FormatMessagesOptions{})
		return "", fmt.Errorf("esbuild errors:\n%s", msgs)
	}

	if len(result.OutputFiles) == 0 {
		return "", fmt.Errorf("esbuild produced no output")
	}

	return string(result.OutputFiles[0].Contents), nil
}
