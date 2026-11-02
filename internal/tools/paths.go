package tools

// tempFilePattern derives a CreateTemp pattern for a sibling of base that is
// visually tied to it, so a leftover temp file is recognizable.
func tempFilePattern(base, tool string) string {
	return base + "." + tool + "-tmp-*"
}
