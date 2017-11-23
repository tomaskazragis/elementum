package memory

import (
	"errors"
	"io"
	"math"
	"sync"
	"time"
)

// CHUNK_SIZE Size of Chunk, comes from anacrolix/torrent
const CHUNK_SIZE = 1024 * 16

type Buffer struct {
	c *Cache
	p *Piece

	mu sync.Mutex
}

func (me *Buffer) IsPositioned() bool {
	if me.p == nil || me.c == nil || !me.p.Active || me.p.Position == -1 {
		return false
	}

	return true
}

// Seek File-like implementation
func (me *Buffer) Seek(offset int64, whence int) (ret int64, err error) {
	return
}

// Write File-like implementation
func (me *Buffer) Write(b []byte) (n int, err error) {
	return
}

// WriteAt File-like implementation
func (me *Buffer) WriteAt(b []byte, off int64) (n int, err error) {
	// me.p.mu.Lock()
	// defer me.p.mu.Unlock()

	if !me.IsPositioned() {
		log.Debugf("Not positioned: %#v", me.p)
		return 0, errors.New("Not positioned")
	}

	chunkId, _ := me.GetChunkForOffset(off)
	me.p.Chunks.AddInt(chunkId)
	me.p.Size += int64(len(b))

	n = copy(me.c.buffers[me.p.Position][off:], b[:])

	me.p.Size += int64(n)
	return
}

// Close File-like implementation
func (me *Buffer) Close() error {
	return nil
}

// Stat File-like implementation
func (me *Buffer) Stat() (*Stats, error) {
	return &Stats{
		path:     me.p.Hash,
		size:     me.Size(),
		modified: me.Modified(),
	}, nil
}

// Read File-like implementation
func (me *Buffer) Read(b []byte) (n int, err error) {
	return
}

// ReadAt File-like implementation
func (me *Buffer) ReadAt(b []byte, off int64) (n int, err error) {
	// me.p.mu.Lock()
	// defer me.p.mu.Unlock()

	if !me.IsPositioned() {
		log.Debugf("Not aligned ReadAt: %#v", me.p.Index)
		return 0, io.EOF
		// return 0, errors.New("Not positioned")
	}

	requested := len(b)
	startIndex, _ := me.GetChunkForOffset(off)
	lastIndex, _ := me.GetChunkForOffset(off + int64(requested-CHUNK_SIZE))

	if lastIndex < startIndex {
		lastIndex = startIndex
	}

	// me.c.mu.Lock()
	// defer me.c.mu.Unlock()

	for i := startIndex; i <= lastIndex; i++ {
		if !me.p.Chunks.ContainsInt(i) {
			log.Debugf("ReadAt not contains: %#v -- %#v -- %#v -- %#v -- %#v", me.p.Hash, off, i, startIndex, lastIndex)
			return 0, errors.New("Chunk not available")
		}
	}

	n = copy(b, me.c.buffers[me.p.Position][off:][:])
	if n != requested {
		log.Debugf("ReadAt return: %#v -- %#v -- %#v -- %#v -- %#v -- %#v", me.p.Hash, off, n, requested, startIndex, lastIndex)
		return 0, errors.New("Output size differs")
	}

	return n, nil
}

func (me *Buffer) Size() int64 {
	// me.p.mu.Lock()
	// defer me.p.mu.Unlock()

	if !me.IsPositioned() || !me.p.Completed {
		return 0
	}

	return me.p.Size
}

func (me *Buffer) Modified() time.Time {
	// me.p.mu.Lock()
	// defer me.p.mu.Unlock()

	if !me.IsPositioned() {
		return time.Now()
	}

	return me.p.Modified
}

func (me *Buffer) GetChunkForOffset(offset int64) (index, margin int) {
	index = int(math.Ceil(float64(offset) / float64(CHUNK_SIZE)))
	margin = int(math.Mod(float64(offset), float64(CHUNK_SIZE)))
	if margin > 0 {
		index--
	}

	return
}
