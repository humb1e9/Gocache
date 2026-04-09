package value

// ByteView stores cached data as an immutable byte slice.
type ByteView struct {
	b []byte
}

// NewByteView clones the input bytes and returns an immutable view.
func NewByteView(b []byte) ByteView {
	return ByteView{b: cloneBytes(b)}
}

// Len returns the view length in bytes.
func (v ByteView) Len() int {
	return len(v.b)
}

// ByteSlice returns a copy to keep the underlying bytes immutable.
func (v ByteView) ByteSlice() []byte {
	return cloneBytes(v.b)
}

// String converts the cached value to string.
func (v ByteView) String() string {
	return string(v.b)
}

func cloneBytes(b []byte) []byte {
	c := make([]byte, len(b))
	copy(c, b)
	return c
}
