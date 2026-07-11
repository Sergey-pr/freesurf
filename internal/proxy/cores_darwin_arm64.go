package proxy

import "embed"

//go:embed cores/darwin-arm64
var coresFS embed.FS

const coresSubdir = "cores/darwin-arm64"
