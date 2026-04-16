package main

// Version info injected at build time via ldflags:
//
//	-X main.Version=$(git describe --tags --always --dirty)
//	-X main.Commit=$(git rev-parse --short HEAD)
//	-X main.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

func main() {
	Execute(Version, Commit, BuildTime)
}
