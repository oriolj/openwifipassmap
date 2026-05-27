// Package wifi shells out to the OS to scan for and connect to networks.
//
//   - Linux: NetworkManager via `nmcli` (must be installed and managing WiFi).
//   - macOS: `networksetup` for connect. Scanning is unreliable since macOS
//     14.4 removed the `airport` tool; we best-effort parse system_profiler.
//
// These are native-only operations; they are not exercised by the web/e2e tests.
package wifi

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// DefaultInterface is the macOS WiFi device most Macs use.
const DefaultInterface = "en0"

// Scan returns the SSIDs of nearby networks (deduplicated, non-empty).
func Scan() ([]string, error) {
	switch runtime.GOOS {
	case "linux":
		return scanLinux()
	case "darwin":
		return scanDarwin()
	default:
		return nil, fmt.Errorf("wifi scan not supported on %s", runtime.GOOS)
	}
}

// Connect joins ssid using password (empty for open networks).
func Connect(ssid, password string) error {
	switch runtime.GOOS {
	case "linux":
		return connectLinux(ssid, password)
	case "darwin":
		return connectDarwin(ssid, password)
	default:
		return fmt.Errorf("wifi connect not supported on %s", runtime.GOOS)
	}
}

func scanLinux() ([]string, error) {
	// -t terse, -f SSID field; one SSID per line (rescans implicitly).
	out, err := exec.Command("nmcli", "-t", "-f", "SSID", "dev", "wifi", "list").Output()
	if err != nil {
		return nil, fmt.Errorf("nmcli scan failed (is NetworkManager installed?): %w", err)
	}
	return uniqueNonEmpty(strings.Split(string(out), "\n")), nil
}

func connectLinux(ssid, password string) error {
	args := []string{"dev", "wifi", "connect", ssid}
	if password != "" {
		args = append(args, "password", password)
	}
	out, err := exec.Command("nmcli", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("nmcli connect failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func scanDarwin() ([]string, error) {
	// macOS 14.4 removed the `airport` CLI; system_profiler is the fallback.
	out, err := exec.Command("system_profiler", "SPAirPortDataType").Output()
	if err != nil {
		return nil, fmt.Errorf("macOS scan failed: %w", err)
	}
	// system_profiler indents a network's NAME one level under its section
	// header and indents that network's PROPERTIES one level deeper still. We
	// collect the direct children of the network sections (the SSIDs, which may
	// legitimately contain spaces) and skip the deeper property lines. This is
	// best-effort and untested in CI (no macOS available).
	lines := strings.Split(string(out), "\n")
	var ssids []string
	for i, line := range lines {
		if h := strings.TrimSpace(line); h != "Other Local Wi-Fi Networks:" && h != "Current Network Information:" {
			continue
		}
		sectionIndent := indentOf(line)
		netIndent := -1
		for j := i + 1; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == "" {
				continue
			}
			ind := indentOf(lines[j])
			if ind <= sectionIndent {
				break // dedent past the section header → section ended
			}
			if netIndent == -1 {
				netIndent = ind // first child establishes the SSID indent level
			}
			if ind == netIndent {
				ssids = append(ssids, strings.TrimSuffix(strings.TrimSpace(lines[j]), ":"))
			}
		}
	}
	return uniqueNonEmpty(ssids), nil
}

func indentOf(s string) int {
	return len(s) - len(strings.TrimLeft(s, " \t"))
}

func connectDarwin(ssid, password string) error {
	args := []string{"-setairportnetwork", DefaultInterface, ssid}
	if password != "" {
		args = append(args, password)
	}
	out, err := exec.Command("networksetup", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("networksetup failed: %s", strings.TrimSpace(string(out)))
	}
	// networksetup prints errors to stdout with a 0 exit code.
	if msg := strings.TrimSpace(string(out)); msg != "" {
		return fmt.Errorf("networksetup: %s", msg)
	}
	return nil
}

func uniqueNonEmpty(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
