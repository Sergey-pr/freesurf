package proxy

import "embed"

//go:embed cores/windows-arm64
var coresFS embed.FS

const coresSubdir = "cores/windows-arm64"
