package tools

import (
	"os"
	"path/filepath"
)

// resolveWriteTarget returns the path a write to `path` should land on.
//
// Both write_file and edit_file replace their target with a renamed temp file
// to make the write atomic, and a rename onto a symlink swaps the link for a
// regular file instead of updating the file it points at. For `~/.bashrc` or
// `~/.gitconfig` symlinked into a dotfiles repository — a very common layout —
// that silently destroys the user's link and leaves an untracked copy behind.
//
// Resolving the link first keeps the tool's promise ("edit this file") pointed
// at the real file, and the temp file is created beside that file so the rename
// stays on one device.
//
// Paths that are not symlinks, and links that cannot be resolved (a broken
// link whose target does not exist yet), are returned unchanged: replacing such
// a path with a real file is the most useful reading of the request.
func resolveWriteTarget(path string) string {
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

// tempFilePattern derives a CreateTemp pattern for a sibling of base that is
// visually tied to it, so a leftover temp file is recognizable.
func tempFilePattern(base, tool string) string {
	return base + "." + tool + "-tmp-*"
}
