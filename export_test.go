package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// holdingWriter is what decides whether a failed export can still answer with
// a clean status, so the transition from "held, still answerable" to
// "committed, only truncation remains" is the behaviour worth pinning.

func TestHoldingWriterHoldsEverythingUnderBudget(t *testing.T) {
	var sink bytes.Buffer
	commits := 0
	w := &holdingWriter{w: &sink, hold: 16, commit: func() { commits++ }}

	if _, err := w.Write([]byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if commits != 0 {
		t.Fatalf("commit ran before the budget was spent: %d", commits)
	}
	if w.Committed() {
		t.Fatal("reported committed while still holding")
	}
	if sink.Len() != 0 {
		t.Fatalf("wrote %d bytes through while holding", sink.Len())
	}

	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if commits != 1 {
		t.Fatalf("commit ran %d times, want 1", commits)
	}
	if got := sink.String(); got != "hello" {
		t.Fatalf("held bytes reached the writer as %q", got)
	}
}

func TestHoldingWriterCommitsOnceBudgetIsExceeded(t *testing.T) {
	var sink bytes.Buffer
	commits := 0
	w := &holdingWriter{w: &sink, hold: 8, commit: func() { commits++ }}

	// Two writes inside the budget, then one that spends it.
	for _, chunk := range []string{"aaaa", "bbbb", "cccc"} {
		n, err := w.Write([]byte(chunk))
		if err != nil {
			t.Fatalf("write %q: %v", chunk, err)
		}
		if n != len(chunk) {
			t.Fatalf("write %q reported %d bytes", chunk, n)
		}
	}
	if !w.Committed() {
		t.Fatal("still holding after the budget was exceeded")
	}
	if commits != 1 {
		t.Fatalf("commit ran %d times, want 1", commits)
	}

	// Everything after the commit goes straight through, and the held
	// prefix must have arrived ahead of it, in order.
	if _, err := w.Write([]byte("dddd")); err != nil {
		t.Fatalf("write after commit: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if commits != 1 {
		t.Fatalf("close re-committed: %d", commits)
	}
	if got, want := sink.String(), "aaaabbbbccccdddd"; got != want {
		t.Fatalf("streamed %q, want %q", got, want)
	}
}

func TestHoldingWriterBudgetIsInclusive(t *testing.T) {
	// A render that lands exactly on the budget is still answerable: the
	// point of the budget is what must be held, not what must commit.
	var sink bytes.Buffer
	w := &holdingWriter{w: &sink, hold: 4, commit: func() {}}

	if _, err := w.Write([]byte("abcd")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if w.Committed() {
		t.Fatal("committed on a write that only reached the budget")
	}
}

// TestHoldingWriterNeverBuffersAnOversizedWrite pins the reason the budget is
// checked before the append: a single write bigger than the whole budget must
// go straight through rather than being copied into a buffer that is about to
// be released anyway.
func TestHoldingWriterNeverBuffersAnOversizedWrite(t *testing.T) {
	var sink bytes.Buffer
	w := &holdingWriter{w: &sink, hold: 16, commit: func() {}}

	if _, err := w.Write([]byte("abc")); err != nil {
		t.Fatalf("write: %v", err)
	}

	oversized := strings.Repeat("y", 1024)
	if _, err := w.Write([]byte(oversized)); err != nil {
		t.Fatalf("oversized write: %v", err)
	}
	if held := w.buf.Len(); held != 0 {
		t.Fatalf("held %d bytes of an oversized write", held)
	}
	// The held prefix must still have arrived ahead of it.
	if got, want := sink.String(), "abc"+oversized; got != want {
		t.Fatalf("wrote %d bytes in the wrong order or shape", len(got))
	}

	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestHoldingWriterReportsUnderlyingWriteFailures(t *testing.T) {
	sentinel := errors.New("connection went away")
	w := &holdingWriter{w: failingWriter{err: sentinel}, hold: 4, commit: func() {}}

	// The write that spends the budget is the one that flushes the held
	// bytes, so it is where the failure surfaces.
	_, err := w.Write([]byte("abcdefgh"))
	if !errors.Is(err, sentinel) {
		t.Fatalf("write error = %v, want %v", err, sentinel)
	}
	if !w.Committed() {
		t.Fatal("a failed release left the response uncommitted")
	}
}

func TestHoldingWriterCloseReportsFlushFailure(t *testing.T) {
	sentinel := errors.New("connection went away")
	w := &holdingWriter{w: failingWriter{err: sentinel}, hold: 64, commit: func() {}}

	if _, err := w.Write([]byte("held")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(); !errors.Is(err, sentinel) {
		t.Fatalf("close error = %v, want %v", err, sentinel)
	}
}

// TestHoldingWriterStreamsPastTheBudget is the property the change exists
// for: an export far larger than the budget must never be resident in the
// writer all at once.
func TestHoldingWriterStreamsPastTheBudget(t *testing.T) {
	var sink bytes.Buffer
	w := &holdingWriter{w: &sink, hold: 32, commit: func() {}}

	chunk := strings.Repeat("x", 64)
	for range 100 {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatalf("write: %v", err)
		}
		if held := w.buf.Len(); held > w.hold {
			t.Fatalf("held %d bytes, budget is %d", held, w.hold)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if got, want := sink.Len(), 100*len(chunk); got != want {
		t.Fatalf("streamed %d bytes, want %d", got, want)
	}
}

type failingWriter struct{ err error }

func (f failingWriter) Write([]byte) (int, error) { return 0, f.err }
