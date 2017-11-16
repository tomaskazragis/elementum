package cache

import (
	"errors"
	// "io"
	"math"
	"sync"
	"time"
	// "github.com/elgatito/elementum/storage/memory/filebuffer"
)

// CHUNK_SIZE Size of Chunk, comes from anacrolix/torrent
const CHUNK_SIZE = 1024 * 16

// File virtual file struct
type File struct {
	c *Cache

	position int
	key      key
	path     string

	onRead     func(n int)
	afterWrite func(endOff int64)

	mu     sync.Mutex
	offset int64
}

func (me *File) IsPositioned() bool {
	// if me.bufferId == -1 {
	// 	return false
	// } else if p, ok := me.c.pieces[me.key]; !ok {
	// 	return false
	// } else if p.Position == -1 {
	if p, ok := me.c.pieces[me.key]; !ok {
		return false
	} else if p.Position == -1 {
		return false
	}

	return true
}

// Seek File-like implementation
func (me *File) Seek(offset int64, whence int) (ret int64, err error) {
	// if !me.IsPositioned() {
	// 	return 0, io.EOF
	// }
	//
	// if me.Size() > offset {
	// 	return 0, io.EOF
	// }
	// // ret, err = me.f.Seek(offset, whence)
	// // if err != nil {
	// // 	return
	// // }
	// me.offset = ret
	// return offset, nil
	return
}

// Write File-like implementation
func (me *File) Write(b []byte) (n int, err error) {
	// log.Debugf("Write: %#v -- %#v", len(b), me.offset)
	// n, err = me.f.Write(b)
	// me.offset += int64(n)
	// me.afterWrite(me.offset)
	// log.Debugf("WRITE call: %#v", me)
	return
}

// WriteAt File-like implementation
func (me *File) WriteAt(b []byte, off int64) (n int, err error) {
	me.c.pieces[me.key].mu.Lock()
	defer me.c.pieces[me.key].mu.Unlock()

	if !me.IsPositioned() {
		log.Debugf("Not positioned: %#v", me.c.pieces[me.key])
		return 0, errors.New("Not positioned")
	}

	chunkId, _ := me.GetChunkForOffset(off)
	// log.Debugf("WriteAt: %#v -- %#v -- %#v -- %#v -- %#v", me.path, off, chunkId, me.c.pieces[me.key].Size, me.position)
	me.c.pieces[me.key].Chunks.AddInt(chunkId)
	me.c.pieces[me.key].Size += int64(len(b))

	n = copy(me.c.buffers[me.position][off:], b[:])

	me.afterWrite(off + int64(n))
	return
}

// Close File-like implementation
func (me *File) Close() error {
	// return me.f.Close()
	return nil
}

// Stat File-like implementation
func (me *File) Stat() (*Stats, error) {
	return &Stats{
		path:     me.path,
		size:     me.Size(),
		modified: me.Modified(),
	}, nil
}

// Read File-like implementation
func (me *File) Read(b []byte) (n int, err error) {
	// // n, err = me.f.Read(b)
	// // me.onRead(n)
	// log.Debugf("READ call: %#v", me)
	return
}

// ReadAt File-like implementation
func (me *File) ReadAt(b []byte, off int64) (n int, err error) {
	me.c.pieces[me.key].mu.Lock()
	defer me.c.pieces[me.key].mu.Unlock()

	if !me.IsPositioned() {
		log.Debugf("Not aligned ReadAt: %#v", me)
		return 0, errors.New("Not positioned")
	}

	requested := len(b)
	startIndex, _ := me.GetChunkForOffset(off)
	lastIndex, _ := me.GetChunkForOffset(off + int64(requested-CHUNK_SIZE))

	if lastIndex < startIndex {
		lastIndex = startIndex
	}

	for i := startIndex; i <= lastIndex; i++ {
		if !me.c.pieces[me.key].Chunks.ContainsInt(i) {
			log.Debugf("ReadAt not contains: %#v -- %#v -- %#v -- %#v -- %#v", me.path, off, i, startIndex, lastIndex)
			return 0, errors.New("Chunk not available")
		}
	}

	// log.Debugf("R2a: %#v, %#v, %#v, %#v, %#v", index, len(f.Chunks), len(f.Chunks[i]), cap(f.Chunks[i]), n)

	// n, err = me.f.ReadAt(b, off)
	// me.onRead(n)
	//n = copy(b, me.c.buffers[me.bufferId][off:off+int64(requested)])
	n = copy(b, me.c.buffers[me.position][off:][:])
	// log.Debugf("ReadAt n: %#v -- %#v -- %#v -- %#v -- %#v -- %#v", me.path, off, requested, n, startIndex, lastIndex)
	if n != requested {
		log.Debugf("ReadAt return: %#v -- %#v -- %#v -- %#v -- %#v -- %#v", me.path, off, n, requested, startIndex, lastIndex)
		return 0, errors.New("Output size differs")
	}

	me.onRead(n)
	return n, nil
}

func (me *File) Size() int64 {
	me.c.pieces[me.key].mu.Lock()
	defer me.c.pieces[me.key].mu.Unlock()

	if !me.IsPositioned() {
		return 0
	}

	// log.Debugf("Size: %#v, %#v, %#v, %#v", me.path, me.key, me.c.pieces[me.key].Size, me.c.pieces[me.key])
	return me.c.pieces[me.key].Size
}

func (me *File) Modified() time.Time {
	me.c.pieces[me.key].mu.Lock()
	defer me.c.pieces[me.key].mu.Unlock()

	if !me.IsPositioned() {
		return time.Now()
	}

	return me.c.pieces[me.key].Accessed
}

func (me *File) GetChunkForOffset(offset int64) (index, margin int) {
	index = int(math.Ceil(float64(offset) / float64(CHUNK_SIZE)))
	margin = int(math.Mod(float64(offset), float64(CHUNK_SIZE)))
	if margin > 0 {
		index--
	}

	return
}
