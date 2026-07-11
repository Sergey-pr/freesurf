package proxy

import "embed"

//go:embed cores/windows-386
var coresFS embed.FS

const coresSubdir = "cores/windows-386"
