package applog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTailLinesReturnsTheLastN(t *testing.T) {
	path := filepath.Join(t.TempDir(), "console.log")
	var b strings.Builder
	for i := 1; i <= 50; i++ {
		b.WriteString(strings.Repeat("x", i%7+1))
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o640); err != nil {
		t.Fatal(err)
	}

	lines, size, err := TailLines(path, 5)
	if err != nil {
		t.Fatal(err)
	}
	if size == 0 {
		t.Fatal("size is 0")
	}
	if len(lines) != 5 {
		t.Fatalf("got %d lines, want 5: %#v", len(lines), lines)
	}
	if !strings.Contains(lines[4], "x") {
		t.Errorf("last line %q looks empty", lines[4])
	}
}

func TestTailLinesHandlesCRLFAndNoTrailingNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "console.log")
	if err := os.WriteFile(path, []byte("one\r\ntwo\r\nthree"), 0o640); err != nil {
		t.Fatal(err)
	}

	lines, _, err := TailLines(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"one", "two", "three"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Errorf("lines = %#v, want %#v", lines, want)
	}
}

func TestReadSinceAdvances(t *testing.T) {
	path := filepath.Join(t.TempDir(), "console.log")
	if err := os.WriteFile(path, []byte("hello\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	got, next, err := ReadSince(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello\n" {
		t.Errorf("first read = %q", got)
	}

	more, next2, err := ReadSince(path, next)
	if err != nil {
		t.Fatal(err)
	}
	if len(more) != 0 {
		t.Errorf("second read without new data = %q", more)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("world\n")
	_ = f.Close()

	got, _, err = ReadSince(path, next2)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "world\n" {
		t.Errorf("third read = %q, want world\\n", got)
	}
}

func TestReadSinceResetsWhenFileShrinks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "console.log")
	if err := os.WriteFile(path, []byte("abcdefghijklmnopqrstuvwxyz"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("short"), 0o640); err != nil {
		t.Fatal(err)
	}

	got, next, err := ReadSince(path, 26)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "short" {
		t.Errorf("got %q after a truncation, want the new contents", got)
	}
	if next != 5 {
		t.Errorf("next = %d, want 5", next)
	}
}
