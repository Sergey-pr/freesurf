package proxy

import "embed"

//go:embed cores/windows-amd64
var coresFS embed.FS

const coresSubdir = "cores/windows-amd64"
