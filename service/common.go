package service

import (
	"errors"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// safeJoinUnderBase resolves `name` relative to `base` and verifies that the
// cleaned absolute path is still contained within `base`. It defends against
// path-traversal payloads like `../../etc/shadow` reaching the file IO RPCs.
//
// `base` must be an absolute path. `name` may contain forward slashes; it is
// treated as a relative path under `base` and rejected if it tries to escape.
//
// Returns the cleaned, base-relative absolute path on success.
func safeJoinUnderBase(base, name string) (string, error) {
	if !filepath.IsAbs(base) {
		return "", errors.New("internal error: base path is not absolute")
	}
	if name == "" {
		return "", errors.New("filename is empty")
	}
	if filepath.IsAbs(name) {
		return "", errors.New("filename must be relative")
	}
	cleanedBase := filepath.Clean(base)
	cleanedJoined := filepath.Clean(filepath.Join(cleanedBase, name))
	// Ensure cleanedJoined is the base itself or a descendant of it.
	// Compare with a separator suffix on base to avoid "/foo" matching "/foobar".
	if cleanedJoined != cleanedBase &&
		!strings.HasPrefix(cleanedJoined, cleanedBase+string(filepath.Separator)) {
		return "", errors.New("path traversal rejected: '" + name + "' escapes '" + base + "'")
	}
	// The check above is purely LEXICAL -- filepath.Clean does not consult the
	// filesystem -- so on its own it proves nothing about where the path leads
	// once the kernel resolves symlinks. One of the allowed bases is /tmp,
	// which is world-writable, so any local user can leave a symlink in it for
	// a root-owned service to follow. That was demonstrated: a non-root user
	// created /tmp/sub/escalate -> /etc/cron.d/pwned and FSWriteFile wrote a
	// root-owned cron job through it.
	//
	// The kernel's fs.protected_symlinks does NOT cover this. It refuses to
	// follow a symlink owned by someone else only when the symlink sits
	// directly in a STICKY world-writable directory; a symlink one level down,
	// inside an ordinary directory the attacker created, is followed normally.
	if err := verifyResolvesUnderBase(cleanedBase, cleanedJoined); err != nil {
		return "", err
	}
	return cleanedJoined, nil
}

// verifyResolvesUnderBase resolves symlinks on the deepest existing ancestor
// of candidate and confirms it is still inside base.
//
// It walks upwards because the candidate frequently does not exist yet -- a
// write creates it. Whatever the deepest existing ancestor is, THAT is what
// the kernel will traverse, so that is what must be contained. If the leaf
// itself exists and is a symlink, EvalSymlinks resolves it here; if it is a
// dangling symlink, EvalSymlinks fails with ENOENT and the O_NOFOLLOW on the
// open in common/filesys.go catches it instead. The two checks cover each
// other's blind spot.
func verifyResolvesUnderBase(base, candidate string) error {
	realBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		// A base that does not resolve is a deployment problem, not an attack.
		return errors.New("cannot resolve allowed directory '" + base + "': " + err.Error())
	}
	probe := candidate
	for {
		resolved, err := filepath.EvalSymlinks(probe)
		if err == nil {
			if resolved != realBase &&
				!strings.HasPrefix(resolved, realBase+string(filepath.Separator)) {
				return errors.New("path traversal rejected: '" + candidate +
					"' resolves through a symlink to '" + resolved +
					"', outside '" + base + "'")
			}
			return nil
		}
		if !os.IsNotExist(err) {
			return err
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			// Walked to the filesystem root without finding anything that
			// exists. Nothing can be traversed, so nothing can escape.
			return nil
		}
		probe = parent
	}
}

// safePathInAllowlist returns the cleaned form of an absolute path iff that
// cleaned path lives under at least one of the provided allowed base dirs.
// Used by RPCs that accept absolute paths over the wire (legacy callers).
//
// Rejects relative paths, empty paths, and anything that resolves outside
// every allowed base after symlink-less Clean().
func safePathInAllowlist(absPath string, allowedBases ...string) (string, error) {
	if absPath == "" {
		return "", errors.New("path is empty")
	}
	if !filepath.IsAbs(absPath) {
		return "", errors.New("path must be absolute")
	}
	cleaned := filepath.Clean(absPath)
	for _, base := range allowedBases {
		cleanedBase := filepath.Clean(base)
		if cleaned == cleanedBase ||
			strings.HasPrefix(cleaned, cleanedBase+string(filepath.Separator)) {
			return cleaned, nil
		}
	}
	return "", errors.New("path '" + absPath + "' is outside the allowed directories")
}

func binaryAvailable(binname string) bool {
	// exec.LookPath is preferred over shelling out to `which`: it's portable,
	// doesn't spawn a subprocess, and uses the same $PATH resolution semantics
	// as exec.Command itself.
	_, err := exec.LookPath(binname)
	return err == nil
}

func ListFilesOfFolder(folderPath string, allowedExtensions ...string) (res []string, err error) {
	// assure all allowed extensions are prepended with a dot and converted to lower case
	for i, e := range allowedExtensions {
		if len(e) == 0 {
			continue
		}
		if []rune(e)[0] != '.' {
			allowedExtensions[i] = "." + allowedExtensions[i]
		}
		allowedExtensions[i] = strings.ToLower(allowedExtensions[i])
	}

	fcontent, err := ioutil.ReadDir(folderPath)
	if err != nil {
		return res, err
	}

	for _, fitem := range fcontent {
		if !fitem.IsDir() {
			extensionValid := false
			// seems to be a file
			if len(allowedExtensions) > 0 {
				//check if extension is valid
				itemExt := strings.ToLower(filepath.Ext(fitem.Name()))
			Inner:
				for _, validExt := range allowedExtensions {
					if validExt == itemExt {
						extensionValid = true
						break Inner
					}
				}
			} else {
				extensionValid = true
			}

			if extensionValid {
				res = append(res, fitem.Name())
			}

		}
	}

	return
}
