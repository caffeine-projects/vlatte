package bufpool

import (
	"bytes"
	"sync"
)

const maxBufSize = 1 << 16

var bufPool = &sync.Pool{
	New: func() any { return bytes.NewBuffer(make([]byte, 0, 500)) },
}

func Get() *bytes.Buffer {
	return bufPool.Get().(*bytes.Buffer)
}

func Put(b *bytes.Buffer) {
	if b.Cap() > maxBufSize {
		return
	}

	bufPool.Put(b)
}
