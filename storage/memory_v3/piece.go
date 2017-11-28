package memory

import (
	"errors"
	"io"
	"math"
	"sync"
	"time"

	"github.com/RoaringBitmap/roaring"
	"github.com/anacrolix/torrent/storage"
)

// CHUNK_SIZE Size of Chunk, comes from anacrolix/torrent
const CHUNK_SIZE = 1024 * 16

// Piece stores meta information about buffer contents
type Piece struct {
	c *Cache

	Index     int
	Key       key
	Length    int64
	Position  int
	Hash      string
	Active    bool
	Completed bool
	Size      int64
	Accessed  time.Time

	Chunks *roaring.Bitmap
	mu     sync.Mutex
}

func (p *Piece) Completion() storage.Completion {
	return storage.Completion{
		Complete: p.Active && p.Completed && p.Size == p.Length && p.Length != 0,
		Ok:       true,
	}
}

func (p *Piece) MarkComplete() error {
	p.Completed = true
	return nil
}

func (p *Piece) MarkNotComplete() error {
	p.Completed = false
	return nil
}

func (p *Piece) OpenBuffer(iswrite bool) (ret bool, err error) {
	p.c.mu.Lock()
	defer p.c.mu.Unlock()

	if p.Index >= len(p.c.pieces) {
		return false, errors.New("Piece index not valid")
	}

	if !p.c.pieces[p.Key].Active && iswrite {
		for index, v := range p.c.positions {
			if v.Used {
				continue
			}

			v.Used = true
			v.Index = p.Index
			v.Key = p.Key

			selected := p.c.pieces[p.Key]

			selected.Position = index
			selected.Active = true
			selected.Size = 0

			p.c.items[p.Key] = itemState{}

			break
		}

		if !p.c.pieces[p.Key].Active {
			log.Debugf("Buffer not assigned: %#v", p.c.positions)
			return false, errors.New("Could not assign buffer")
		}
	}

	// p.c.mu.Lock()
	// defer p.c.mu.Unlock()
	p.c.updateItem(p.Key, func(i *itemState, ok bool) bool {
		if !ok {
			*i = p.GetState()
		}
		i.Accessed = time.Now()
		return ok
	})

	return true, nil
}

func (p *Piece) IsPositioned() bool {
	if p == nil || p.c == nil || !p.Active || p.Position == -1 {
		return false
	}

	return true
}

// Seek File-like implementation
func (p *Piece) Seek(offset int64, whence int) (ret int64, err error) {
	return
}

// Write File-like implementation
func (p *Piece) Write(b []byte) (n int, err error) {
	return
}

// WriteAt File-like implementation
func (p *Piece) WriteAt(b []byte, off int64) (n int, err error) {
	buf, err := p.OpenBuffer(true)
	if err != nil || !buf {
		return 0, err
	}

	// me.p.mu.Lock()
	// defer me.p.mu.Unlock()

	if !p.IsPositioned() {
		log.Debugf("Not positioned: %#v", p)
		return 0, errors.New("Not positioned")
	}

	chunkId, _ := p.GetChunkForOffset(off)
	p.Chunks.AddInt(chunkId)

	n = copy(p.c.buffers[p.Position][off:], b[:])

	p.Size += int64(n)
	p.onWrite()
	return
}

// Close File-like implementation
func (p *Piece) Close() error {
	return nil
}

// Read File-like implementation
func (p *Piece) Read(b []byte) (n int, err error) {
	return
}

// ReadAt File-like implementation
func (p *Piece) ReadAt(b []byte, off int64) (n int, err error) {
	buf, err := p.OpenBuffer(false)
	if err != nil || !buf {
		return 0, nil
	}

	// me.p.mu.Lock()
	// defer me.p.mu.Unlock()

	if !p.IsPositioned() {
		return 0, io.EOF
	}

	requested := len(b)
	startIndex, _ := p.GetChunkForOffset(off)
	lastIndex, _ := p.GetChunkForOffset(off + int64(requested-CHUNK_SIZE))

	if lastIndex < startIndex {
		lastIndex = startIndex
	}

	// me.c.mu.Lock()
	// defer me.c.mu.Unlock()

	for i := startIndex; i <= lastIndex; i++ {
		if !p.Chunks.ContainsInt(i) {
			return 0, io.EOF
		}
	}

	n = copy(b, p.c.buffers[p.Position][off:][:])
	if n != requested {
		return 0, io.EOF
	}

	p.onRead()
	return n, nil
}

func (p *Piece) GetChunkForOffset(offset int64) (index, margin int) {
	index = int(math.Ceil(float64(offset) / float64(CHUNK_SIZE)))
	margin = int(math.Mod(float64(offset), float64(CHUNK_SIZE)))
	if margin > 0 {
		index--
	}

	return
}

func (p *Piece) GetState() itemState {
	return itemState{
		Size:     p.Size,
		Accessed: p.Accessed,
	}
}

func (p *Piece) onRead() {
	p.c.mu.Lock()
	defer p.c.mu.Unlock()
	p.c.updateItem(p.Key, func(i *itemState, ok bool) bool {
		i.Accessed = time.Now()
		return ok
	})
}

func (p *Piece) onWrite() {
	p.c.mu.Lock()
	defer p.c.mu.Unlock()
	p.c.updateItem(p.Key, func(i *itemState, ok bool) bool {
		i.Accessed = time.Now()
		i.Size = p.Size
		return ok
	})
}
