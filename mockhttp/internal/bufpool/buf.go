package bufpool

import (
	"bytes"
	"sync"
)

const maxBufSize = 1 << 16

var bufPool = &sync.Pool{
	New: func() any { return newBuffer() },
}

func Get() *bytes.Buffer {
	if b, ok := bufPool.Get().(*bytes.Buffer); ok {
		return b
	}

	return newBuffer()
}

func Put(b *bytes.Buffer) {
	if b.Cap() > maxBufSize {
		return
	}

	bufPool.Put(b)
}

func newBuffer() *bytes.Buffer {
	return bytes.NewBuffer(make([]byte, 0, 500))
}
