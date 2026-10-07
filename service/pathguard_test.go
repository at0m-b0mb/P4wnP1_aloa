//go:build linux
// +build linux

package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mame82/P4wnP1_aloa/common"
	pb "github.com/mame82/P4wnP1_aloa/proto"
)

// These tests exist because the file-IO RPCs run as root and one of their
// three allowed directories is /tmp, which is world-writable. The lexical
// containment check that was here originally -- filepath.Clean plus a prefix
// comparison -- is correct about the STRING and says nothing about where the
// path leads once the kernel resolves symlinks.
//
// The escape was demonstrated end to end against a running service: a non-root
// user created /tmp/sub/escalate -> /etc/cron.d/pwned, and
// FSWriteFile(folder=TMP, filename="sub/escalate") wrote a root-owned cron job
// through it. Writing a cron drop-in as root is root code execution.
//
// fs.protected_symlinks does not save you here. It only refuses symlinks that
// sit DIRECTLY in a sticky world-writable directory and are owned by someone
// other than the following process. A symlink one level down, inside an
// ordinary directory the attacker created, is followed normally -- which is
// exactly what the demonstrated escape used.

func TestSafeJoinRejectsLexicalTraversal(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{
		"../etc/shadow",
		"../../etc/p4wnp1/auth.json",
		"a/../../b",
		"/etc/shadow",
		"",
	} {
		if got, err := safeJoinUnderBase(base, name); err == nil {
			t.Errorf("safeJoinUnderBase(%q) = %q, want an error", name, got)
		}
	}
}

func TestSafeJoinAcceptsOrdinaryNames(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{"f.txt", "sub/f.txt", "./f.txt", "a/b/c/d.bin"} {
		got, err := safeJoinUnderBase(base, name)
		if err != nil {
			t.Errorf("safeJoinUnderBase(%q) errored: %v", name, err)
			continue
		}
		if !strings.HasPrefix(got, base) {
			t.Errorf("safeJoinUnderBase(%q) = %q, outside base %q", name, got, base)
		}
	}
}

// The leaf itself is a symlink pointing out of the base.
func TestSafeJoinRejectsSymlinkedLeaf(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("sensitive"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(base, "link")); err != nil {
		t.Fatal(err)
	}
	if got, err := safeJoinUnderBase(base, "link"); err == nil {
		t.Fatalf("a symlinked leaf was accepted: %q", got)
	}
}

// This is the demonstrated escape: the symlink is NOT in the sticky base, it
// is one level down in an ordinary subdirectory, which is why the kernel's
// fs.protected_symlinks does not stop it.
func TestSafeJoinRejectsSymlinkInSubdirectory(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(t.TempDir(), "cronjob")

	sub := filepath.Join(base, "sub")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(sub, "escalate")); err != nil {
		t.Fatal(err)
	}

	// The target does not exist yet -- a dangling symlink -- which is exactly
	// the case a write-to-create attack uses. This must still be refused.
	if got, err := safeJoinUnderBase(base, "sub/escalate"); err == nil {
		// safeJoinUnderBase may legitimately allow a dangling leaf through
		// (EvalSymlinks cannot resolve it); the O_NOFOLLOW on the open is the
		// backstop. Prove the backstop actually holds.
		if err := common.WriteFile(got, false, false, []byte("* * * * * root id\n"), 0600); err == nil {
			t.Fatalf("wrote through a dangling symlink to %q -- arbitrary root write", outside)
		}
		if _, err := os.Stat(outside); err == nil {
			t.Fatalf("the write landed outside the base at %q", outside)
		}
	}
}

// Same shape, but the symlink target already exists, so EvalSymlinks resolves
// it and safeJoinUnderBase itself must refuse.
func TestSafeJoinRejectsResolvableSymlinkInSubdirectory(t *testing.T) {
	base := t.TempDir()
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "target")
	if err := os.WriteFile(outside, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}

	sub := filepath.Join(base, "sub")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(sub, "escalate")); err != nil {
		t.Fatal(err)
	}

	if got, err := safeJoinUnderBase(base, "sub/escalate"); err == nil {
		t.Fatalf("a symlink inside a subdirectory was accepted: %q", got)
	}
}

// A symlinked DIRECTORY component. O_NOFOLLOW does not cover this -- it only
// guards the final component -- so the containment check must.
func TestSafeJoinRejectsSymlinkedDirectoryComponent(t *testing.T) {
	base := t.TempDir()
	outsideDir := t.TempDir()
	if err := os.Symlink(outsideDir, filepath.Join(base, "jump")); err != nil {
		t.Fatal(err)
	}
	if got, err := safeJoinUnderBase(base, "jump/anything.txt"); err == nil {
		t.Fatalf("a symlinked directory component was accepted: %q", got)
	}
}

// A base that is itself reached through a symlink must still work: /tmp is a
// symlink to /private/tmp on some systems, and resolving the base without
// resolving the candidate the same way would reject every legitimate path.
func TestSafeJoinWorksWhenBaseIsItselfASymlink(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "baselink")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := safeJoinUnderBase(link, "ordinary.txt"); err != nil {
		t.Errorf("a symlinked base rejected an ordinary name: %v", err)
	}
}

// The RPC-level entry point must enforce the same thing for every folder.
func TestResolveAccessibleFolderRejectsTraversalForEveryFolder(t *testing.T) {
	folders := []struct {
		name  string
		value pb.AccessibleFolder
	}{
		{"TMP", pb.AccessibleFolder_TMP},
		{"BASH_SCRIPTS", pb.AccessibleFolder_BASH_SCRIPTS},
		{"HID_SCRIPTS", pb.AccessibleFolder_HID_SCRIPTS},
	}
	for _, f := range folders {
		for _, bad := range []string{"../escape", "../../etc/shadow", "/etc/shadow"} {
			if _, _, err := resolveAccessibleFolder(f.value, bad); err == nil {
				t.Errorf("folder %s accepted %q", f.name, bad)
			}
		}
	}
}

// O_NOFOLLOW must be on the read path too, not just the write path: the same
// symlink that grants an arbitrary write grants an arbitrary read, and the
// read leaks /etc/p4wnp1/auth.json and /run/p4wnp1/local.token.
func TestReadFileRefusesToFollowASymlinkedLeaf(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("bcrypt-hash-here"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 32)
	if n, err := common.ReadFile(link, 0, buf, 0600); err == nil {
		t.Fatalf("read %d bytes through a symlink: %q", n, buf[:n])
	}
}

func TestWriteFileRefusesToFollowASymlinkedLeaf(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := common.WriteFile(link, false, false, []byte("overwritten"), 0600); err == nil {
		t.Fatal("wrote through a symlink")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original" {
		t.Fatalf("the target was modified through the symlink: %q", got)
	}
}

// safePathInAllowlist is the OTHER entry point, and it was missed when
// safeJoinUnderBase was hardened. It guards HIDRunScript, HIDRunScriptJob and
// FSGetFileInfo, and /tmp is one of the bases they allow -- so without symlink
// containment a local user plants a link and HIDRunScript reads an arbitrary
// root-readable file and TYPES IT INTO THE ATTACHED HOST. That is exfiltration
// to the machine the device is plugged into, which is the one place it must
// not leak to.
func TestSafePathInAllowlistRejectsSymlinkedLeaf(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("bcrypt-hashes-here"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "innocent.js")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if got, err := safePathInAllowlist(link, base); err == nil {
		t.Fatalf("a symlinked leaf was accepted: %q", got)
	}
}

func TestSafePathInAllowlistRejectsSymlinkedDirectory(t *testing.T) {
	base := t.TempDir()
	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "secret.js"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(base, "jump")); err != nil {
		t.Fatal(err)
	}
	if got, err := safePathInAllowlist(filepath.Join(base, "jump", "secret.js"), base); err == nil {
		t.Fatalf("a symlinked directory component was accepted: %q", got)
	}
}

func TestSafePathInAllowlistStillAcceptsOrdinaryPaths(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "script.js")
	if err := os.WriteFile(real, []byte("layout('US');"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := safePathInAllowlist(real, base); err != nil {
		t.Errorf("an ordinary path inside the base was rejected: %v", err)
	}
	// And a second allowed base must still work -- the real callers pass two.
	other := t.TempDir()
	if _, err := safePathInAllowlist(real, other, base); err != nil {
		t.Errorf("a path in the SECOND allowed base was rejected: %v", err)
	}
	// Outside every base, still refused.
	if _, err := safePathInAllowlist(filepath.Join(t.TempDir(), "x"), base, other); err == nil {
		t.Error("a path outside every base was accepted")
	}
}
