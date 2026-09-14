// Package buildinfo carries the release identifier of the running binary.
//
// Version is set by the linker at image build time (docker/Dockerfile):
//
//	go build -ldflags "-X github.com/oriolj/openwifipassmap/internal/buildinfo.Version=<git sha>"
//
// On Coolify the value comes from the SOURCE_COMMIT build arg (the deployed
// commit). Coolify also injects SOURCE_COMMIT into the container environment,
// which Get falls back to for images built without the flag. Surfaced on
// /api/health, the X-App-Version response header, the
// openwifipassmap_app_info metric and the GlitchTip release tag, so every
// signal can answer "which deploy is this?".
package buildinfo

import "os"

// Version is the git short SHA baked in at build time, or "dev" locally.
var Version = "dev"

// Get returns the release identifier: the linker-set Version, else the
// SOURCE_COMMIT env (shortened), else "dev".
func Get() string {
	if Version != "" && Version != "dev" {
		return Version
	}
	if v := os.Getenv("SOURCE_COMMIT"); v != "" {
		if len(v) > 12 {
			v = v[:12]
		}
		return v
	}
	return Version
}
