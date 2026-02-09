package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
)

func tailFileLines(path string, n int, maxBytes int64) (string, error) {
	if n <= 0 {
		return "", nil
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	size := st.Size()
	if maxBytes <= 0 || maxBytes > size {
		maxBytes = size
	}

	start := size - maxBytes
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, 0); err != nil {
		return "", err
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return tailBytesLines(b, n), nil
}

func tailBytesLines(b []byte, n int) string {
	if n <= 0 {
		return ""
	}
	// Count lines from end.
	c := 0
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] == '\n' {
			c++
			if c == n+1 { // include partial line before the last N newlines
				return string(bytes.TrimLeft(b[i+1:], "\n"))
			}
		}
	}
	return string(bytes.TrimLeft(b, "\n"))
}
