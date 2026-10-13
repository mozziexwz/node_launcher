//go:build !bundle

package main

import "embed"

// Development builds load the redirector runtime from beside the executable.
var bundledRuntime embed.FS

const runtimeBundled = false
