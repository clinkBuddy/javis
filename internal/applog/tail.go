// Package applog reads the redirected stdout/stderr of a managed process.
package applog

import (
	"io"
	"os"
)

// TailLines returns the last n lines of a file. The file is read from the
// end in chunks so a multi-megabyte console log does not have to be loaded
// just to show the recent output.
func TailLines(path string, n int) (lines []string, size int64, err error) {
	if n <= 0 {
		n = 200
	}
	if n > 2000 {
		n = 2000
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	size = info.Size()
	if size == 0 {
		return []string{}, 0, nil
	}

	const chunk = 32 * 1024
	buf := make([]byte, 0, chunk)
	remaining := size
	need := n + 1 // +1 because a trailing newline produces an empty last split

	for remaining > 0 && countNewlines(buf) < need {
		read := int64(chunk)
		if remaining < read {
			read = remaining
		}
		remaining -= read
		block := make([]byte, read)
		if _, err := f.ReadAt(block, remaining); err != nil && err != io.EOF {
			return nil, size, err
		}
		buf = append(block, buf...)
	}

	all := splitLines(buf)
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all, size, nil
}

func countNewlines(b []byte) int {
	n := 0
	for _, c := range b {
		if c == '\n' {
			n++
		}
	}
	return n
}

func splitLines(b []byte) []string {
	if len(b) == 0 {
		return []string{}
	}
	out := []string{}
	start := 0
	for i, c := range b {
		if c != '\n' {
			continue
		}
		line := b[start:i]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		out = append(out, string(line))
		start = i + 1
	}
	if start < len(b) {
		line := b[start:]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		if len(line) > 0 {
			out = append(out, string(line))
		}
	}
	return out
}

// ReadSince returns bytes written past offset, advancing it. Used by the
// streaming endpoint so each poll only ships new output.
func ReadSince(path string, offset int64) (data []byte, next int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, offset, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, offset, err
	}
	size := info.Size()
	// A truncated or rotated file starts over. Console logs are one file per
	// launch, so this mainly covers an operator emptying the file by hand.
	if offset > size {
		offset = 0
	}
	if offset == size {
		return nil, size, nil
	}

	buf := make([]byte, size-offset)
	if _, err := f.ReadAt(buf, offset); err != nil && err != io.EOF {
		return nil, offset, err
	}
	return buf, size, nil
}
