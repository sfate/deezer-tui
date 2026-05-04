//go:build !darwin

package player

func NewProcessBackend() *BeepBackend {
	return NewBeepBackend()
}
