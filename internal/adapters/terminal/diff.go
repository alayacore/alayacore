package terminal

// LCS-based diff algorithm for computing line-level diffs between old and new content.
// Used by the edit_file display handler to show changed lines with +/- prefixes.

// diffPair represents a pair of old/new lines in a diff
type diffPair struct {
	old string
	new string
}

// maxDiffLines caps the LCS matrix size for edit_file diffs. The
// old/new strings come from the model's tool-call arguments and can be
// arbitrarily large — an unbounded m×n DP would exhaust memory on huge
// inputs (10k × 10k lines = 800MB of matrix cells). FormatCall falls
// back to a degenerate "every line changed" diff above this cap.
const maxDiffLines = 2000

// computeDiff computes the LCS-based diff between old and new lines
func computeDiff(oldLines, newLines []string) []diffPair {
	lcs := computeLCS(oldLines, newLines)

	var result []diffPair
	i, j := 0, 0

	for _, lcsLine := range lcs {
		for i < len(oldLines) && oldLines[i] != lcsLine {
			result = append(result, diffPair{old: oldLines[i], new: ""})
			i++
		}

		for j < len(newLines) && newLines[j] != lcsLine {
			result = append(result, diffPair{old: "", new: newLines[j]})
			j++
		}

		if i < len(oldLines) && j < len(newLines) {
			result = append(result, diffPair{old: oldLines[i], new: newLines[j]})
			i++
			j++
		}
	}

	for i < len(oldLines) {
		result = append(result, diffPair{old: oldLines[i], new: ""})
		i++
	}
	for j < len(newLines) {
		result = append(result, diffPair{old: "", new: newLines[j]})
		j++
	}

	return result
}

// computeLCS computes the Longest Common Subsequence of two string slices
func computeLCS(a, b []string) []string {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}

	m, n := len(a), len(b)
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				dp[i][j] = max(dp[i-1][j], dp[i][j-1])
			}
		}
	}

	// Walk the table backward collecting the LCS, then reverse: appending
	// is O(n) total, whereas prepending each element would be O(n²).
	var lcs []string
	i, j := m, n
	for i > 0 && j > 0 {
		switch {
		case a[i-1] == b[j-1]:
			lcs = append(lcs, a[i-1])
			i--
			j--
		case dp[i-1][j] > dp[i][j-1]:
			i--
		default:
			j--
		}
	}
	for l, r := 0, len(lcs)-1; l < r; l, r = l+1, r-1 {
		lcs[l], lcs[r] = lcs[r], lcs[l]
	}

	return lcs
}
