//go:build ignore

// yaml-block-scalars-ours.go — this repository's half of
// misc/check-yaml-block-scalars.sh.
//
// It reads the case table on stdin, hands each case to this repository's own
// manifest reader wrapped as a SKILL.md, and prints one line per case in the
// shape the yaml.v3 oracle prints — so the driver diffs two files of values
// rather than reading either side itself. Problems go to stderr, because a case
// this reader claims to read exactly as YAML does is wrong if it also has
// something to report.
//
// A dev tool in the shape misc/tlvcat.go already has: run with `go run`, and
// kept out of the module's build by the ignore tag. The other half cannot live
// here — yaml.v3 is deliberately not a dependency of this module (see
// docs/dependencies.md) — so the driver builds it in a scratch module of its
// own.

package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/alayacore/alayacore/internal/skills"
)

func main() {
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		label, inner, ok := strings.Cut(line, "\t")
		if !ok {
			fmt.Fprintf(os.Stderr, "case without a label: %q\n", line)
			os.Exit(2)
		}
		meta, _, problems, err := skills.ParseSkillMarkdown("---\nname: x\n" + unescape(inner) + "\n---\nbody\n")
		for _, p := range problems {
			fmt.Fprintf(os.Stderr, "%s: %s\n", label, p)
		}
		if err != nil {
			fmt.Printf("%s\tERR: %s\n", label, strings.ReplaceAll(err.Error(), "\n", " / "))
			continue
		}
		fmt.Printf("%s\t%q\n", label, meta.Description)
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "reading the cases: %v\n", err)
		os.Exit(2)
	}
}

// unescape turns the table's spelled-out line breaks back into the bytes a
// manifest has, so a case stays one line of the table and still carries a block.
func unescape(s string) string {
	s = strings.ReplaceAll(s, `\t`, "\t")
	return strings.ReplaceAll(s, `\n`, "\n")
}
