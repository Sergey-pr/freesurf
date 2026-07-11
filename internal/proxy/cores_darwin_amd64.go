package proxy

import "embed"

//go:embed cores/darwin-amd64
var coresFS embed.FS

const coresSubdir = "cores/darwin-amd64"
