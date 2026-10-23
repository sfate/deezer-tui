//go:build !darwin && cgo

package player

func NewProcessBackend() *MalgoBackend {
	return NewMalgoBackend()
}
