package memory

import (
	"errors"
	"io"
	"time"
	// "math"
	// "sync"

	"github.com/RoaringBitmap/roaring"
	"github.com/anacrolix/torrent/storage"
)

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
}

// Completion ...
func (p *Piece) Completion() storage.Completion {
	return storage.Completion{
		Complete: p.Active && p.Completed && p.Size == p.Length && p.Length != 0,
		Ok:       true,
	}
}

// MarkComplete ...
func (p *Piece) MarkComplete() error {
	log.Debugf("Complete: %#v", p.Index)
	p.Completed = true
	return nil
}

// MarkNotComplete ...
func (p *Piece) MarkNotComplete() error {
	log.Debugf("NotComplete: %#v", p.Index)
	p.Completed = false
	return nil
}

// OpenBuffer ...
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

			p.c.items[p.Key] = ItemState{}

			break
		}

		if !p.c.pieces[p.Key].Active {
			log.Debugf("Buffer not assigned: %#v", p.c.positions)
			return false, errors.New("Could not assign buffer")
		}
	}

	p.c.updateItem(p.Key, func(i *ItemState, ok bool) bool {
		if !ok {
			*i = p.GetState()
		}
		i.Accessed = time.Now()
		return ok
	})

	return true, nil
}

// IsPositioned ...
func (p *Piece) IsPositioned() bool {
	if p == nil || p.c == nil || !p.Active || p.Position == -1 {
		return false
	}

	return true
}

// Seek File-like implementation
func (p *Piece) Seek(offset int64, whence int) (ret int64, err error) {
	log.Debugf("Seek lone: %#v", offset)
	return
}

// Write File-like implementation
func (p *Piece) Write(b []byte) (n int, err error) {
	log.Debugf("Write lone: %#v", len(b))
	return
}

// WriteAt File-like implementation
func (p *Piece) WriteAt(b []byte, off int64) (n int, err error) {
	p.c.bmu.Lock()

	buf, err := p.OpenBuffer(true)
	if err != nil || !buf {
		log.Debugf("Can't get buffer write: %#v", p.Index)
		p.c.bmu.Unlock()
		return 0, err
	}

	p.c.bmu.Unlock()

	if !p.IsPositioned() {
		log.Debugf("Not positioned write: %#v", p.Index)
		return 0, errors.New("Not positioned")
	}

	p.c.buffers[p.Position].mu.Lock()

	chunkID, _ := p.GetChunkForOffset(off)
	p.Chunks.AddInt(chunkID)

	n = copy(p.c.buffers[p.Position].body[off:], b[:])

	p.Size += int64(n)
	p.onWrite()
	p.c.buffers[p.Position].mu.Unlock()
	return
}

// Close File-like implementation
func (p *Piece) Close() error {
	return nil
}

// Read File-like implementation
func (p *Piece) Read(b []byte) (n int, err error) {
	log.Debugf("Read lone: %#v", len(b))
	return
}

// ReadAt File-like implementation
func (p *Piece) ReadAt(b []byte, off int64) (n int, err error) {
	p.c.bmu.Lock()

	buf, err := p.OpenBuffer(false)
	if err != nil || !buf {
		log.Debugf("No buffer read: %#v", p.Index)
		p.c.bmu.Unlock()
		return 0, nil
		// return 0, io.EOF
	}

	p.c.bmu.Unlock()

	if !p.IsPositioned() {
		log.Debugf("No position read: %#v", p.Index)
		return 0, io.EOF
	}

	p.c.buffers[p.Position].mu.Lock()

	requested := len(b)
	startIndex, _ := p.GetChunkForOffset(off)
	lastIndex, _ := p.GetChunkForOffset(off + int64(requested-ChunkSize))

	if lastIndex < startIndex {
		lastIndex = startIndex
	}

	// log.Debugf("Read: %#v: %#v, O: %#v, I: %#v-%#v", p.Index, requested, off, startIndex, lastIndex)

	// me.c.mu.Lock()
	// defer me.c.mu.Unlock()

	for i := startIndex; i <= lastIndex; i++ {
		if !p.Chunks.ContainsInt(i) {
			log.Debugf("No contain read: %#v", p.Index)
			p.c.buffers[p.Position].mu.Unlock()
			return 0, io.EOF
		}
	}

	// n = copy(b, p.c.buffers[p.Position][off:][:])
	n = copy(b, p.c.buffers[p.Position].body[off:][:])
	if n != requested {
		log.Debugf("No matched read: %#v", p.Index)
		p.c.buffers[p.Position].mu.Unlock()
		return 0, io.EOF
	}

	p.onRead()
	p.c.buffers[p.Position].mu.Unlock()
	return n, nil
}

// GetChunkForOffset ...
func (p *Piece) GetChunkForOffset(offset int64) (index, margin int) {
	index = int(offset / ChunkSize)
	margin = int(offset % ChunkSize)

	return
}

// GetState ...
func (p *Piece) GetState() ItemState {
	return ItemState{
		Size:     p.Size,
		Accessed: p.Accessed,
	}
}

func (p *Piece) onRead() {
	p.c.mu.Lock()
	defer p.c.mu.Unlock()
	p.c.updateItem(p.Key, func(i *ItemState, ok bool) bool {
		i.Accessed = time.Now()
		return ok
	})
}

func (p *Piece) onWrite() {
	p.c.mu.Lock()
	defer p.c.mu.Unlock()
	p.c.updateItem(p.Key, func(i *ItemState, ok bool) bool {
		i.Accessed = time.Now()
		i.Size = p.Size
		return ok
	})
}
