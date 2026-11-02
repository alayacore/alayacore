// Package atomicfile commits a file's new content with a rename, so a reader
// sees either the old version or the new one and never a partial write.
//
// Replacing a file in place — open, truncate, write — has two windows that a
// reader can catch: one where the file is empty and one where it holds a prefix.
// A crash inside either leaves that state on disk, and a full disk leaves it
// too. A second writer makes it worse than a window: its bytes land in the same
// range, so the file ends up holding a mix of both contents rather than one of
// them.
//
// Two writers is the case that matters here. AlayaCore's config files
// (runtime.conf, model.conf, the theme defaults, MCP token files) live in a
// per-user directory, and two instances started from the same --config-path
// write that directory independently. internal/tools/fileMutationMu serializes
// writers inside one process only; the files in this package's care are outside
// its reach.
//
// There are two entry points, differing only in what they do when the *directory*
// cannot hold a temp file: Write (atomic or an error) and WriteBestEffort (which
// falls back to the in-place write described above). See their docs for which
// callers get which, and why.
package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
)

// Write replaces the contents of path with data, atomically, or reports why it
// could not.
//
// perm is the mode for a file that does not exist yet, narrowed by the process
// umask exactly as os.WriteFile would narrow it; an existing file keeps its own
// mode, so a rewrite cannot drop bits the user deliberately set (a 0755 script
// would stop running under a 0077 umask). Because a rename needs permission on
// the directory rather than on the file, an existing file is replaced even when
// it is read-only — the same trade vim and nano make.
//
// A symlink is written through rather than replaced: the temp file is created
// beside the link's target, so the link still points at the file it named (see
// ResolveSymlink). A hard link is not: the rename replaces one directory entry,
// and the other names keep the old content.
//
// Write is safe to call concurrently, from this process or others: each call
// commits a temp file of its own, and last writer wins on the target. It does not
// create the parent directory — callers that own a fresh config directory create
// it first, and a write that failed for a missing directory would otherwise look
// like it had somewhere to go.
//
// A directory that cannot hold a temp file is an error here, not a reason to
// write in place: this is the call for files AlayaCore owns, and for them a
// non-atomic write is the defect rather than the fallback. WriteBestEffort is the
// variant for a caller that must succeed.
func Write(path string, data []byte, perm os.FileMode) error {
	_, err := write(path, data, perm, false)
	return err
}

// WriteBestEffort is Write with a weaker fallback: when the directory cannot hold
// a temp file, it truncates the target and writes into it instead of failing, and
// reports that it did (inPlace).
//
// That write is NOT atomic — a reader can catch the file empty or half-written,
// and two writers can interleave their bytes in the same range. inPlace exists so
// the caller can say so: a weaker guarantee is acceptable only when it is stated,
// not when the caller believes it got the atomic one. It is for callers whose
// promise is "write the file the user named" — internal/tools/write_file.go, where
// the alternative is refusing to touch a writable file that sits in a read-only
// directory (a real layout: a vendored tree, or a service config owned by root
// with a group-writable file), and where refusal would only push the write into a
// shell command. AlayaCore's own config files call Write instead: for them,
// reporting the failure is better than a file another process can read
// half-written.
func WriteBestEffort(path string, data []byte, perm os.FileMode) (inPlace bool, err error) {
	return write(path, data, perm, true)
}

// write is the implementation both entry points share. Its bool reports that the
// in-place fallback was taken, which only WriteBestEffort allows.
func write(path string, data []byte, perm os.FileMode, inPlaceFallback bool) (bool, error) {
	target := ResolveSymlink(path)

	// One Stat decides both the mode and whether the in-place fallback below is
	// allowed. A second Stat would only look like it validated the target, while
	// the file could change between the two calls.
	var existing bool
	if info, err := os.Stat(target); err == nil {
		switch {
		case info.IsDir():
			return false, fmt.Errorf("cannot write to %s: it is a directory", path)
		case info.Mode().IsRegular():
			existing = true
			perm = info.Mode().Perm()
		}
	}

	dir, base := filepath.Split(target)
	if dir == "" {
		dir = "."
	}

	// The temp file is a sibling: renaming across filesystems fails, and a
	// sibling is the only location guaranteed to be on the destination's device.
	tmp, err := os.CreateTemp(dir, base+".tmp-*")
	if err != nil {
		// A rename needs write permission on the *directory*; an in-place write
		// never did, it only needed the file itself to be writable. WriteBestEffort
		// keeps such a write reachable — degraded, not denied — and Write refuses
		// it, so that its callers never get a non-atomic write they did not ask
		// for.
		if inPlaceFallback && existing {
			return true, writeInPlace(target, data)
		}
		return false, fmt.Errorf("create temp file for %s: %w", target, err)
	}
	tmpPath := tmp.Name()

	// chmod bypasses the umask, so a new file's mode has to be narrowed here.
	// Applying the literal perm would silently WIDEN permissions for anyone
	// running under a restrictive umask.
	if !existing {
		perm &^= currentUmask()
	}

	if err := writeAndClose(tmp, data, perm); err != nil {
		os.Remove(tmpPath)
		return false, err
	}

	// Commits the new content or leaves the old file untouched. On Windows a
	// target held open without delete sharing makes the rename fail with the old
	// version still in place: an error the caller reports, not a corrupt file it
	// does not.
	if err := os.Rename(tmpPath, target); err != nil {
		os.Remove(tmpPath)
		return false, fmt.Errorf("replace %s: %w", target, err)
	}

	return false, nil
}

// writeAndClose fills the temp file, restores the target's mode, and flushes it
// to stable storage before the caller renames it into place. Without the sync a
// power loss can leave the renamed file present but empty: the rename is atomic,
// but it does not order the data write behind it.
func writeAndClose(f *os.File, data []byte, perm os.FileMode) error {
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	// Best effort: some filesystems reject fsync and the data is already
	// written, so failing here would be worse than a weaker guarantee.
	_ = f.Sync()
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(f.Name(), perm); err != nil {
		return fmt.Errorf("set mode of temp file: %w", err)
	}
	return nil
}

// writeInPlace replaces an existing file's content without a rename. It is the
// fallback for a directory that cannot hold a temp file, and keeps the behavior
// such a layout had before this package existed.
//
// Weaker guarantee — a reader can catch the truncation, and a crash mid-write can
// leave a partial file — but on those layouts the alternative is refusing the
// write, not a safer one. The inode is reused, so the file keeps its own mode
// and ownership without a chmod.
func writeInPlace(target string, data []byte) error {
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("write %s: %w", target, err)
	}
	_ = f.Sync() // best effort; see writeAndClose
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", target, err)
	}
	return nil
}

// ResolveSymlink returns the file a path really names when the path is a
// symlink, so that a caller replacing the file replaces the target rather than
// the link itself: a config file symlinked out of a dotfiles repository must stay
// a symlink. A path that is not a link, or a link that cannot be resolved, is
// returned as given, so a caller then fails with the OS's own reason instead of
// this package's guess at one.
func ResolveSymlink(path string) string {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return path
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return resolved
}
