package player

import (
	"io"
)

type StreamBuffer struct {
	receiver <-chan []byte
	buffer   []byte
	position int
	closed   bool
}

func NewStreamBuffer(receiver <-chan []byte) *StreamBuffer {
	return &StreamBuffer{
		receiver: receiver,
		buffer:   []byte{},
		position: 0,
		closed:   false,
	}
}

func (s *StreamBuffer) Read(out []byte) (int, error) {
	if len(out) == 0 {
		return 0, nil
	}

	want := s.position + len(out)
	s.fillTo(want)

	available := len(s.buffer) - s.position
	if available <= 0 {
		return 0, io.EOF
	}

	n := min(len(out), available)
	copy(out[:n], s.buffer[s.position:s.position+n])
	s.position += n
	return n, nil
}

func (s *StreamBuffer) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		target := max0(int(offset))
		s.fillTo(target)
		s.position = min(target, len(s.buffer))
	case io.SeekCurrent:
		target := max0(s.position + int(offset))
		s.fillTo(target)
		s.position = min(target, len(s.buffer))
	case io.SeekEnd:
		for !s.closed {
			chunk, ok := <-s.receiver
			if !ok {
				s.closed = true
				break
			}
			if len(chunk) > 0 {
				s.buffer = append(s.buffer, chunk...)
			}
		}
		target := max0(len(s.buffer) + int(offset))
		s.position = min(target, len(s.buffer))
	default:
		return 0, io.ErrUnexpectedEOF
	}
	return int64(s.position), nil
}

func (s *StreamBuffer) fillTo(need int) {
	for len(s.buffer) < need && !s.closed {
		chunk, ok := <-s.receiver
		if !ok {
			s.closed = true
			break
		}
		if len(chunk) == 0 {
			continue
		}
		s.buffer = append(s.buffer, chunk...)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max0(v int) int {
	if v < 0 {
		return 0
	}
	return v
}
