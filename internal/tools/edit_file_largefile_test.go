package tools

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestEditFileLargeFileMemoryBounded(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "large_test.txt")

	file, err := os.Create(testFile)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	targetSize := 10 * 1024 * 1024
	chunk := make([]byte, 1024*1024)
	for i := range chunk {
		chunk[i] = byte('A' + (i % 26))
	}

	pattern := []byte("UNIQUE_PATTERN_TO_REPLACE")
	patternPos := targetSize / 2

	written := 0
	for written < targetSize {
		toWrite := len(chunk)
		if written+toWrite > targetSize {
			toWrite = targetSize - written
		}

		if written <= patternPos && written+toWrite > patternPos {
			part1 := patternPos - written
			if part1 > 0 {
				if _, wErr := file.Write(chunk[:part1]); wErr != nil {
					t.Fatalf("Failed to write: %v", wErr)
				}
				written += part1
			}
			if _, wErr := file.Write(pattern); wErr != nil {
				t.Fatalf("Failed to write pattern: %v", wErr)
			}
			written += len(pattern)
			remaining := toWrite - part1
			if remaining > 0 {
				if _, wErr := file.Write(chunk[:remaining]); wErr != nil {
					t.Fatalf("Failed to write: %v", wErr)
				}
				written += remaining
			}
		} else {
			if _, wErr := file.Write(chunk[:toWrite]); wErr != nil {
				t.Fatalf("Failed to write: %v", wErr)
			}
			written += toWrite
		}
	}
	file.Close()

	var m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m1)

	_, err = executeEditFile(context.Background(), EditFileInput{
		Path:      testFile,
		OldString: string(pattern),
		NewString: "REPLACED_SUCCESSFULLY",
	})

	var m2 runtime.MemStats
	runtime.ReadMemStats(&m2)

	if err != nil {
		t.Fatalf("executeEditFile failed: %v", err)
	}

	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read edited file: %v", err)
	}

	if !bytes.Contains(content, []byte("REPLACED_SUCCESSFULLY")) {
		t.Error("Replacement text not found in file")
	}

	if bytes.Contains(content, pattern) {
		t.Error("Original pattern still exists in file")
	}

	memIncrease := int64(m2.Alloc) - int64(m1.Alloc)
	maxAllowed := int64(20 * 1024 * 1024)

	if memIncrease > 0 {
		t.Logf("Memory increase: %.2f MB", float64(memIncrease)/1024/1024)
		if memIncrease > maxAllowed {
			t.Errorf("Memory usage too high: %.2f MB (max allowed: %.2f MB)",
				float64(memIncrease)/1024/1024,
				float64(maxAllowed)/1024/1024)
		}
	} else {
		t.Logf("Memory decreased (GC ran): %.2f MB", float64(-memIncrease)/1024/1024)
	}
}
