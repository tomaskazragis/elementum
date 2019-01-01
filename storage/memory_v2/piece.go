package memory

import (
	"errors"
	"io"
	"time"

	"github.com/anacrolix/missinggo/perf"
	"github.com/anacrolix/sync"
	"github.com/anacrolix/torrent/storage"
)

// Piece stores meta information about buffer contents
type Piece struct {
	c *Cache
	b *Buffer

	mu *sync.RWMutex

	index  int
	length int64

	completed bool
	size      int64
	read      bool
}

// Completion ...
func (p *Piece) Completion() storage.Completion {
	p.mu.Lock()
	defer p.mu.Unlock()

	return storage.Completion{
		Complete: p.completed,
		Ok:       true,
	}
}

// MarkComplete ...
func (p *Piece) MarkComplete() error {
	defer perf.ScopeTimer()()

	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.buffered() || p.length == 0 || p.size == 0 {
		log.Debugf("Complete Error: %#v == nil || !%#v || !%#v", p.buffered(), p.length == 0, p.size == 0)
		p.Reset()
		return errors.New("piece is not complete")
	}

	p.completed = true

	// log.Debugf("Complete: %#v", p.index)

	return nil
}

// MarkNotComplete ...
func (p *Piece) MarkNotComplete() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.completed = false
	p.read = false
	p.size = 0

	// log.Debugf("Not complete: %#v", p.index)

	return nil
}

func (p *Piece) buffered() bool {
	return p.b != nil
}

func (p *Piece) getReadBuffer() bool {
	return p.getBuffer(false)
}

func (p *Piece) getWriteBuffer() bool {
	return p.getBuffer(true)
}

func (p *Piece) getBuffer(isWrite bool) bool {
	defer perf.ScopeTimer()()

	if p.buffered() {
		return true
	} else if p.index >= len(p.c.pieces) {
		return false
	}

	// defer func() {
	// 	if p.buffered() {
	// 		log.Debugf("Assigned buffer for (%v): %v of %v, %v, %v", isWrite, p.index, p.length, p.b.index, cap(p.b.buffer))
	// 	} else {
	// 		log.Debugf("Not assigned buffer for (%v): %v of %v, %v", isWrite, p.index, p.length, p.buffered())
	// 	}
	// }()

	if !p.buffered() && isWrite {
		p.c.bmu.Lock()
		defer p.c.bmu.Unlock()

		for _, b := range p.c.buffers {
			if b.used {
				continue
			}

			b.used = true
			b.pi = p.index
			b.accessed = time.Now()

			p.b = p.c.buffers[b.index]

			// If we are placing permanent buffer entry - we should reduce the limit,
			// to propely check for the usage.
			if p.c.reservedPieces.ContainsInt(p.index) {
				p.c.bufferLimit--
			} else {
				p.c.bufferUsed++
			}

			break
		}

		if !p.buffered() {
			log.Debugf("Buffer not assigned!")
			return false
		}
	}

	return p.buffered()
}

// Reset is cleaning stats to 0's
func (p *Piece) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.b = nil
	p.completed = false
	p.read = false
	p.size = 0
}

// WriteAt File-like implementation
func (p *Piece) WriteAt(b []byte, off int64) (n int, err error) {
	defer perf.ScopeTimer()()

	p.mu.Lock()
	defer p.mu.Unlock()

	if buffered := p.getWriteBuffer(); !buffered {
		return 0, errors.New("Can't get buffer write")
	}

	n = copy(p.b.buffer[off:], b[:])

	p.size += int64(n)
	p.b.accessed = time.Now()

	if p.c.bufferUsed >= p.c.bufferLimit {
		go p.c.trim()
	}

	return
}

// ReadAt File-like implementation
func (p *Piece) ReadAt(b []byte, off int64) (n int, err error) {
	defer perf.ScopeTimer()()

	p.mu.RLock()
	defer p.mu.RUnlock()

	if buffered := p.getReadBuffer(); !buffered {
		return 0, io.EOF
	}
	if p.size < p.length {
		return 0, io.ErrUnexpectedEOF
	}

	requested := len(b)

	n = copy(b, p.b.buffer[off:][:])
	if n != requested {
		log.Debugf("Not matched requested size(%#v of %#v): %#v", n, requested, p.index)
		return 0, io.EOF
	}

	if p.completed && off+int64(n) >= p.size {
		p.read = true
	}

	p.b.accessed = time.Now()

	return n, nil
}

// Close File-like implementation
func (p *Piece) Close() error {
	return nil
}
