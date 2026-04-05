package util

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func EnsureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}

func EnsureWritableDir(path string) error {
	if err := EnsureDir(path); err != nil {
		return err
	}

	testFile := filepath.Join(path, ".recon-write-check")
	if err := os.WriteFile(testFile, []byte("ok"), 0o644); err != nil {
		return err
	}

	return os.Remove(testFile)
}

func WriteLinesAtomic(path string, lines []string) error {
	return WriteStringAtomic(path, strings.Join(lines, "\n"), !strings.HasSuffix(path, ".txt"))
}

func WriteTextLinesAtomic(path string, lines []string) error {
	content := strings.Join(lines, "\n")
	if len(lines) > 0 {
		content += "\n"
	}

	return WriteStringAtomic(path, content, false)
}

func WriteStringAtomic(path, content string, noTrailingNewline bool) error {
	if err := EnsureDir(filepath.Dir(path)); err != nil {
		return err
	}

	if !noTrailingNewline && content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}

	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return err
	}

	if err := tmp.Close(); err != nil {
		return err
	}

	if _, err := os.Stat(path); err == nil {
		if removeErr := os.Remove(path); removeErr != nil {
			return fmt.Errorf("remove existing %s: %w", path, removeErr)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	return os.Rename(tmpPath, path)
}

func ReadLines(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}

	return lines, scanner.Err()
}

func ReadFirstNonEmptyLines(path string, limit int) ([]string, error) {
	lines, err := ReadLines(path)
	if err != nil {
		return nil, err
	}

	if len(lines) <= limit {
		return lines, nil
	}

	return lines[:limit], nil
}

func CopyFile(src, dst string) error {
	input, err := os.Open(src)
	if err != nil {
		return err
	}
	defer input.Close()

	if err := EnsureDir(filepath.Dir(dst)); err != nil {
		return err
	}

	output, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer output.Close()

	if _, err := io.Copy(output, input); err != nil {
		return err
	}

	return output.Close()
}

func UniqueSorted(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	unique := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		unique = append(unique, item)
	}
	sort.Strings(unique)
	return unique
}

func UniqueSortedLower(items []string) []string {
	lowered := make([]string, 0, len(items))
	for _, item := range items {
		lowered = append(lowered, strings.ToLower(strings.TrimSpace(item)))
	}
	return UniqueSorted(lowered)
}

func ChunkStrings(items []string, chunkSize int) [][]string {
	if chunkSize <= 0 {
		chunkSize = len(items)
	}

	if len(items) == 0 {
		return nil
	}

	chunks := make([][]string, 0, (len(items)+chunkSize-1)/chunkSize)
	for start := 0; start < len(items); start += chunkSize {
		end := start + chunkSize
		if end > len(items) {
			end = len(items)
		}
		chunk := make([]string, end-start)
		copy(chunk, items[start:end])
		chunks = append(chunks, chunk)
	}
	return chunks
}

func PathWithin(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// FindWritableDirInPATH returns the first directory listed in PATH where the current
// user can create files. This is useful to choose a global install target that
// will be available on the user's shell without modifying PATH.
func FindWritableDirInPATH() (string, error) {
	pathEnv := os.Getenv("PATH")
	if strings.TrimSpace(pathEnv) == "" {
		return "", fmt.Errorf("PATH environment variable is empty")
	}
	parts := strings.Split(pathEnv, string(os.PathListSeparator))
	for _, dir := range parts {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		// Try to create and remove a temp file to validate write access.
		tmp, err := os.CreateTemp(dir, ".recon-perm-test-*")
		if err != nil {
			continue
		}
		tmpName := tmp.Name()
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return dir, nil
	}
	return "", fmt.Errorf("no writable directory found in PATH")
}
