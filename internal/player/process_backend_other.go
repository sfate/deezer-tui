//go:build !darwin && !cgo

package player

func NewProcessBackend() *BeepBackend {
	return NewBeepBackend()
}
