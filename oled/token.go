package oled

import (
	"os"
	"strings"
)

// DefaultTokenPath is where the service writes the credential its own local
// scripts authenticate with. Mirrored here rather than imported from
// service/auth so this package stays buildable on any OS -- the service
// package is linux-only, and the whole point of the UI code is that it runs
// on a laptop too.
const DefaultTokenPath = "/run/p4wnp1/local.token"

// DefaultBaseURL is the service's HTTP API on the device itself.
const DefaultBaseURL = "http://127.0.0.1:8000"

// readLocalToken reads the machine-local credential. A missing file is not an
// error worth distinguishing: either way we have no credential and the caller
// says so on screen.
func readLocalToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
