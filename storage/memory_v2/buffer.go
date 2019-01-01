package memory

import (
	"time"

	"github.com/anacrolix/sync"
)

// Buffer ...
type Buffer struct {
	c  *Cache
	mu *sync.RWMutex

	index    int
	used     bool
	pi       int
	accessed time.Time

	buffer []byte
}

// Reset ...
func (b *Buffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()

	// log.Debugf("Resetting buffer: %d", b.index)

	b.used = false
	b.pi = -1
}

func (b *Buffer) assigned() bool {
	return b.pi != -1
}

func (b *Buffer) reserved() bool {
	return b.c.reservedPieces.ContainsInt(b.pi)
}

func (b *Buffer) readed() bool {
	return b.c.readerPieces.ContainsInt(b.pi)
}
