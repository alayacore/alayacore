// Package checkout asserts the invariants of the working copy the build runs
// in, as distinct from the invariants of the program. Nothing here is imported
// by the application: the tests are the package, and they all ask git about the
// tree rather than assuming what it should look like.
//
// One invariant is load-bearing. gofmt parses CRLF source without complaint and
// prints LF, so it reports every CRLF file as unformatted — which is how the
// Windows CI job failed with 144 findings across the terminal adapter, each
// pointing at line 1, while the Linux job stayed green on the same commits.
// Nothing was wrong with the code: Git for Windows installs with
// core.autocrlf=true, which smudges an LF-committed file to CRLF on checkout,
// and .gitattributes exists to override it.
//
// The assertions are split so each guards one half of the fix:
//
//   - what the index holds            no CR in any stored text blob
//   - what the attributes resolve to  .gitattributes applies, and is complete
//   - what the working copy contains  the checkout gofmt and the tests read
//   - what the fixtures weigh         byte-exact recordings, untouched
//
// Asking git, rather than parsing .gitattributes or walking the tree ourselves,
// is deliberate: our own reading would pass on a rule git never applied. The
// attribute language has precedence, macros and platform defaults that are easy
// to get subtly wrong and impossible to notice on the platform where they are
// harmless — and this package runs on the one where they are not.
package checkout

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// maxListed is how many offenders a failing assertion names before summarizing.
// The incident this package exists for produced 389 findings; a wall of them is
// the failure mode the check replaces, not something it should reproduce.
const maxListed = 10

// trackedPath is one line of `git ls-files -s --eol`: the blob the index holds,
// what git found for the index and the working copy, and the end-of-line
// attributes it resolved for the path.
type trackedPath struct {
	path     string
	blob     string // index blob oid
	indexEOL string // "lf", "crlf", "mixed", or "-text"
	workEOL  string // the same, or "" / "none" / "missing" when absent
	attrs    string // e.g. "text eol=lf", "text=auto eol=lf", "-text"
}

// binary is git's verdict on content, however it reached it: declared by an
// attribute, or inferred from the bytes.
func (p trackedPath) binary() bool { return p.indexEOL == "-text" }

// absentInWorktree covers the ways a tracked file legitimately has no line
// endings to judge — deleted in a dirty tree, or never written by a sparse
// checkout. Neither is an end-of-line finding, and both are worth naming out
// loud so they cannot be mistaken for having passed.
func (p trackedPath) absentInWorktree() bool {
	switch p.workEOL {
	case "", "none", "missing":
		return true
	default:
		return false
	}
}

// tree is the repository as git describes it, plus the root the working copy
// hangs off so that paths can be opened.
type tree struct {
	root  string
	paths []trackedPath
}

// inspect reads the tree in a single git call, so every assertion below reasons
// from one snapshot rather than from several that might disagree.
func inspect(t *testing.T) tree {
	t.Helper()

	root := strings.TrimSpace(git(t, "rev-parse", "--show-toplevel"))
	if root == "" {
		t.Fatal("`git rev-parse --show-toplevel` returned no path")
	}
	// -C is not decoration: `git ls-files` defaults to the current directory and
	// below, which for this package would be a tree of one file.
	out := git(t, "-C", root, "ls-files", "-s", "--eol", "-z", "--full-name")

	records := strings.Split(out, "\x00")
	// One slot per record; empty records (the trailing one after the final NUL)
	// are skipped below rather than compacted after.
	paths := make([]trackedPath, 0, len(records))
	for _, record := range records {
		if record == "" {
			continue
		}
		// "<mode> <oid> <stage>\t<i/…> <w/…> <attr/…>\t<path>"
		parts := strings.SplitN(record, "\t", 3)
		if len(parts) != 3 {
			t.Fatalf("a record from `git ls-files -s --eol` has no path column: %q", record)
		}
		meta := strings.Fields(parts[0])
		if len(meta) != 3 { // mode, oid, stage
			t.Fatalf("cannot read the index entry %q", parts[0])
		}
		cols := strings.Fields(parts[1])
		if len(cols) < 3 {
			t.Fatalf("cannot read the line-ending columns %q", parts[1])
		}
		// The attribute column holds more than one word when more than one
		// attribute matched ("text eol=lf"), and git pads it, so rejoin the tail
		// rather than trusting the field count.
		attrs := strings.TrimSpace(strings.TrimPrefix(strings.Join(cols[2:], " "), "attr/"))

		paths = append(paths, trackedPath{
			path:     parts[2],
			blob:     meta[1],
			indexEOL: strings.TrimPrefix(cols[0], "i/"),
			workEOL:  strings.TrimPrefix(cols[1], "w/"),
			attrs:    attrs,
		})
	}
	if len(paths) == 0 {
		t.Fatal("`git ls-files` tracked no paths; refusing to assert an invariant over nothing")
	}

	return tree{root: root, paths: paths}
}

// TestIndexHoldsOnlyLF says CR never got committed. It is the cheap direction,
// and the one that makes the rest legible: with no CR in the stored blobs, any
// CR in the working copy was put there by the checkout.
//
// `git grep --cached` is asked directly about bytes, and skips what it detects as
// binary, which is the exact set of files this rule is about. The alternative
// reading — the i/ column of `git ls-files --eol` — reports "lf" for a path typed
// as text whose blob holds a CR that is not part of a CRLF pair; gofmt objects to
// that CR, so the column would not be the authority for it.
func TestIndexHoldsOnlyLF(t *testing.T) {
	tr := inspect(t)

	offenders := trackedWithCR(t, tr.root, "--cached")
	report(t, offenders,
		"tracked text blobs that hold a CR.",
		"Once CR is committed, no attribute undoes it for the files already in the tree: every tool that "+
			"compares bytes sees a rewrite, and the checkout can only ever look wrong. Repair a path with "+
			"`git add --renormalize <path>`; the text rules in .gitattributes keep new files from arriving "+
			"that way, whatever core.autocrlf is set to on the machine that commits them.")
}

// TestTextPathsResolveToEndOfLineLF checks the rule rather than the result: that
// git answers eol=lf for every text path, on every platform, whether or not this
// particular checkout happens to look fine today.
//
// It exists for the half fix. `* text=auto` with no eol=lf normalizes the index
// and leaves the working copy to core.autocrlf, whose default on Windows is CRLF
// — so someone can add a .gitattributes, the Linux job stays green, and the
// Windows job goes on failing for the reason it always failed for.
func TestTextPathsResolveToEndOfLineLF(t *testing.T) {
	var offenders []string
	for _, p := range inspect(t).paths {
		if p.binary() {
			continue
		}
		if !strings.Contains(p.attrs, "eol=lf") {
			offenders = append(offenders, fmt.Sprintf("%s: attr/%s", p.path, orNothing(p.attrs)))
		}
	}
	report(t, offenders,
		"tracked text files that no attribute resolves to eol=lf.",
		"Without eol=lf the checkout obeys core.autocrlf, and Git for Windows installs that as true. "+
			"Either .gitattributes has left the tree root, or a rule matching these paths comes later "+
			"than the one that sets eol=lf and overrides it — in .gitattributes the last match wins.")
}

// TestTextFilesAreCheckedOutWithLF is the assertion gofmt would otherwise make
// several hundred times: the checkout handed back no CR.
func TestTextFilesAreCheckedOutWithLF(t *testing.T) {
	tr := inspect(t)

	var absent []string
	for _, p := range tr.paths {
		if !p.binary() && p.absentInWorktree() {
			absent = append(absent, p.path)
		}
	}
	if len(absent) > 0 {
		// A file that is not on disk has no line endings to judge. Say which, so
		// a rebase or a sparse checkout cannot be mistaken for a clean result.
		t.Logf("%d tracked text file(s) are absent from the working copy, so their line endings were "+
			"not judged: %s", len(absent), strings.Join(first(absent, maxListed), ", "))
	}

	offenders := trackedWithCR(t, tr.root)
	report(t, offenders,
		"tracked text files that the checkout gave back with a CR in them.",
		"gofmt parses CRLF and prints LF, so it reports each of these as unformatted at 1:1 — that was "+
			"the Windows job's entire complaint. The index holds no CR, so the checkout is the thing "+
			"adding it, which is exactly what .gitattributes' eol=lf exists to stop. Note that `git "+
			"status` can stay quiet about this: git compares after running the file back through its "+
			"filters, so a CR that gofmt objects to is invisible to it.")
}

// TestNoBinaryIsLeftToContentDetection keeps the catch-all honest. `text=auto`
// decides by peeking at bytes, which is a fair bet for a .txt nobody thought
// about and a bad one for a fixture: git deciding a binary is text lets it
// rewrite what a test replays. Anything binary has to be binary by name.
func TestNoBinaryIsLeftToContentDetection(t *testing.T) {
	var offenders []string
	for _, p := range inspect(t).paths {
		if !p.binary() {
			continue
		}
		if !strings.HasPrefix(p.attrs, "-text") {
			offenders = append(offenders, fmt.Sprintf("%s: attr/%s", p.path, orNothing(p.attrs)))
		}
	}
	report(t, offenders,
		"binary files that are binary only because git guessed.",
		"Name the extension instead: add a rule in the form `*.<ext> binary -eol` so that no file this "+
			"repository depends on byte for byte is one heuristic away from being normalized.")
}

// TestBinaryPathsAreCheckedOutVerbatim is the proof rather than the promise: the
// bytes on disk hash to the blob git stores for them. This is the only assertion
// here that a wrong rule cannot satisfy by looking right — CRs added to a
// recording that has to replay exactly are a different file, not a different
// formatting. And `git status` reports nothing: the conversion it compares
// through is the very thing that made the difference.
func TestBinaryPathsAreCheckedOutVerbatim(t *testing.T) {
	tr := inspect(t)

	var offenders []string
	for _, p := range tr.paths {
		if !p.binary() || p.absentInWorktree() {
			continue
		}
		if len(p.blob) != sha1.Size*2 {
			t.Skipf("index object ids are %d hex characters; this check hashes with SHA-1 the way git's "+
				"blob format does, so it cannot compare them (%s)", len(p.blob), p.path)
		}
		sum, err := hashBlob(filepath.Join(tr.root, filepath.FromSlash(p.path)))
		switch {
		case err != nil:
			offenders = append(offenders, fmt.Sprintf("%s: %v", p.path, err))
		case sum != p.blob:
			offenders = append(offenders, fmt.Sprintf("%s: working copy hashes to %s, index blob is %s",
				p.path, sum, p.blob))
		}
	}
	report(t, offenders,
		"binary paths whose working copy is not the bytes the index stores.",
		"An end-of-line rule is reaching a file it must not touch. adapter-guide/tlv-samples in "+
			"particular are byte-exact TLV recordings whose tests decode them as written.")
}

// git runs git and returns stdout, skipping when the answer is unavailable
// rather than when it is unwelcome: a source tree with no git — a release
// tarball, a module-cache copy — has no checkout to hold to a standard.
func git(t *testing.T, args ...string) string {
	t.Helper()

	out, err := runGit(args...)
	if err != nil {
		t.Skipf("`git %s` failed, so this is probably not a git checkout: %v", strings.Join(args, " "), err)
	}
	return out
}

// trackedWithCR lists the tracked text files whose contents hold a CR — in the
// working copy, or in the index with where set to "--cached". Binary files are
// excluded by git's own detection, which is the set of files line endings can
// legitimately mean nothing to.
//
// git grep exits 1 when it finds nothing, so the absence of results is not an
// error and has to be told apart from one.
func trackedWithCR(t *testing.T, root string, where ...string) []string {
	t.Helper()

	args := append([]string{"-C", root, "grep", "-l", "-I", "-F", "-e", "\r"}, where...)
	out, err := runGit(args...)
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return nil // no match: the invariant holds
		}
		t.Skipf("`git %s` failed: %v", strings.Join(args, " "), err)
	}
	return strings.Split(strings.TrimSpace(out), "\n")
}

func runGit(args ...string) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", err
	}
	// The arguments are fixed subcommands and flags chosen here, never input
	// read from the tree or the environment.
	out, err := exec.Command("git", args...).Output()
	return string(out), err
}

// hashBlob returns git's blob hash for a file: SHA-1 over the "blob <size>"
// header and the raw bytes, with no conversion applied to either.
func hashBlob(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = f.Close() // a read-only handle has nothing left to fail
	}()

	h := sha1.New()
	if _, err := fmt.Fprintf(h, "blob %d\x00", info.Size()); err != nil {
		return "", err
	}
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// report states one finding about one cause: how many paths, which rule they
// broke, what to do. It ends the test, because on a CRLF checkout every later
// assertion here is the same symptom wearing a different hat.
func report(t *testing.T, offenders []string, what, why string) {
	t.Helper()

	if len(offenders) == 0 {
		return
	}
	list := strings.Join(first(offenders, maxListed), "\n\t")
	if len(offenders) > maxListed {
		list += fmt.Sprintf("\n\t... and %d more; the count is the finding, not the list", len(offenders)-maxListed)
	}
	t.Fatalf("%d %s\n\t%s\n\n%s", len(offenders), what, list, why)
}

func first(items []string, n int) []string {
	if len(items) > n {
		return items[:n]
	}
	return items
}

func orNothing(s string) string {
	if s == "" {
		return "(nothing matched: is .gitattributes missing?)"
	}
	return s
}
