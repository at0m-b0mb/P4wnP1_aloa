// p4wnp1-hashpw is a tiny helper used by the first-boot service to bootstrap
// the bcrypt auth file. Bash can't easily produce bcrypt hashes, so we ship
// this binary alongside the main service.
//
// Usage:
//
//	echo -n "secret-password" | p4wnp1-hashpw --output /etc/p4wnp1/auth.json
//	p4wnp1-hashpw --username admin --password-file /tmp/pw --output /etc/p4wnp1/auth.json
//	p4wnp1-hashpw --verify --username admin --password-file /tmp/pw --auth-file /etc/p4wnp1/auth.json
//
// It deliberately uses the same package as the running service so the on-disk
// JSON layout cannot drift between the writer and the reader.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mame82/P4wnP1_aloa/service/auth"
)

func main() {
	var (
		username     = flag.String("username", "admin", "username to set the password for")
		passwordFile = flag.String("password-file", "", "read password from this file (newline trimmed); if empty, read stdin")
		outputPath   = flag.String("output", "/etc/p4wnp1/auth.json", "path to write the auth.json file")
		verify       = flag.Bool("verify", false, "verify mode: load auth-file, check password matches, exit 0 / 1")
		authFile     = flag.String("auth-file", "/etc/p4wnp1/auth.json", "for --verify mode, the file to check against")
	)
	flag.Parse()

	password, err := readPassword(*passwordFile)
	if err != nil {
		die("read password: %v", err)
	}

	if *verify {
		store, err := auth.NewStore(*authFile)
		if err != nil {
			die("load auth file: %v", err)
		}
		if !store.Verify(*username, password) {
			fmt.Fprintln(os.Stderr, "password does not match")
			os.Exit(1)
		}
		fmt.Println("ok")
		return
	}

	// Bootstrap / overwrite. NewStore creates an empty store if outputPath
	// doesn't exist; SetPassword adds the user and persists.
	store, err := auth.NewStore(*outputPath)
	if err != nil {
		die("open auth store at %s: %v", *outputPath, err)
	}
	if err := store.SetPassword(*username, password); err != nil {
		die("set password: %v", err)
	}
	fmt.Fprintf(os.Stderr, "wrote bcrypt-hashed credentials for %q to %s\n", *username, *outputPath)
}

func readPassword(path string) (string, error) {
	var data []byte
	var err error
	if path == "" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return "", err
	}
	// Trim trailing newline(s) -- common when reading from a file.
	return strings.TrimRight(string(data), "\r\n"), nil
}

func die(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "p4wnp1-hashpw: "+format+"\n", args...)
	os.Exit(2)
}
