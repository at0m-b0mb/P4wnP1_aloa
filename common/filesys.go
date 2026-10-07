package common

import (
	"os"
	"syscall"
)

// O_NOFOLLOW refuses to open the final path component if it is a symbolic
// link. The service runs as root and these helpers are reachable from the
// file-IO RPCs, whose allowed directories include /tmp -- a world-writable
// directory any local user can leave a symlink in for root to follow.
//
// This guards the LEAF only; a symlink in a parent directory is still
// followed by the kernel. The containment check in
// service/common.go:safeJoinUnderBase resolves the directory prefix, so the
// two together cover the whole path. Keep both: either alone leaves a hole.
const noFollow = syscall.O_NOFOLLOW

func WriteFile(path string, mustNotExist bool, append bool, data []byte, perm os.FileMode) error {
	flag := os.O_CREATE | os.O_WRONLY | noFollow
	if mustNotExist {
		flag |= os.O_EXCL
	}
	if append {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}
	f, err := os.OpenFile(path, flag, perm)
	// The error check used to come AFTER a call to f.Stat() on the result.
	// That happened to be survivable -- (*os.File).Stat returns ErrInvalid on
	// a nil receiver rather than panicking -- but it read as a nil dereference
	// and its result was discarded, so it was doing nothing except obscuring
	// the error path.
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

func ReadFile(path string, start int64, chunk []byte, perm os.FileMode) (n int, err error) {
	f, err := os.OpenFile(path, os.O_RDONLY|noFollow, perm)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return f.ReadAt(chunk, start)
}
