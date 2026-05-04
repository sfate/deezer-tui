package player

import (
	"io"
	"testing"
)

func TestStreamBufferReadsAcrossChunks(t *testing.T) {
	ch := make(chan []byte, 3)
	ch <- []byte("ab")
	ch <- []byte("cd")
	close(ch)

	buf := NewStreamBuffer(ch)
	out := make([]byte, 4)

	n, err := buf.Read(out)
	if err != nil && err != io.EOF {
		t.Fatalf("read: %v", err)
	}
	if n != 4 || string(out) != "abcd" {
		t.Fatalf("unexpected read result n=%d out=%q", n, string(out))
	}
}

func TestStreamBufferSeekStartAndRead(t *testing.T) {
	ch := make(chan []byte, 2)
	ch <- []byte("hello")
	ch <- []byte("world")
	close(ch)

	buf := NewStreamBuffer(ch)
	if _, err := buf.Seek(7, io.SeekStart); err != nil {
		t.Fatalf("seek start: %v", err)
	}

	out := make([]byte, 3)
	n, err := buf.Read(out)
	if err != nil && err != io.EOF {
		t.Fatalf("read after seek: %v", err)
	}
	if n != 3 || string(out) != "rld" {
		t.Fatalf("unexpected read result n=%d out=%q", n, string(out))
	}
}

func TestStreamBufferSeekEndAfterDrain(t *testing.T) {
	ch := make(chan []byte, 2)
	ch <- []byte("abc")
	ch <- []byte("def")
	close(ch)

	buf := NewStreamBuffer(ch)
	pos, err := buf.Seek(-2, io.SeekEnd)
	if err != nil {
		t.Fatalf("seek end: %v", err)
	}
	if pos != 4 {
		t.Fatalf("unexpected position %d", pos)
	}

	out := make([]byte, 2)
	n, err := buf.Read(out)
	if err != nil && err != io.EOF {
		t.Fatalf("read after seek end: %v", err)
	}
	if n != 2 || string(out) != "ef" {
		t.Fatalf("unexpected read result n=%d out=%q", n, string(out))
	}
}
