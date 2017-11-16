package memory

import (
	"sync"

	"github.com/elgatito/elementum/storage/memory/filebuffer"
)

// File virtual file struct
type File struct {
	f            *filebuffer.Buffer
	path         key
	pathOriginal string
	onRead       func(n int)
	afterWrite   func(endOff int64)
	mu           sync.Mutex
	offset       int64
}

// Seek File-like implementation
func (me *File) Seek(offset int64, whence int) (ret int64, err error) {
	ret, err = me.f.Seek(offset, whence)
	if err != nil {
		return
	}
	me.offset = ret
	return
}

// Write File-like implementation
func (me *File) Write(b []byte) (n int, err error) {
	log.Debugf("Write: %#v -- %#v", len(b), me.offset)
	n, err = me.f.Write(b)
	me.offset += int64(n)
	me.afterWrite(me.offset)
	return
}

// WriteAt File-like implementation
func (me *File) WriteAt(b []byte, off int64) (n int, err error) {
	n, err = me.f.WriteAt(b, off)
	me.afterWrite(off + int64(n))
	return
}

// Close File-like implementation
func (me *File) Close() error {
	return me.f.Close()
}

// Stat File-like implementation
func (me *File) Stat() (*filebuffer.Stats, error) {
	return filebuffer.GetStats(me.pathOriginal, me.f), nil
}

// Read File-like implementation
func (me *File) Read(b []byte) (n int, err error) {
	n, err = me.f.Read(b)
	me.onRead(n)
	return
}

// ReadAt File-like implementation
func (me *File) ReadAt(b []byte, off int64) (n int, err error) {
	n, err = me.f.ReadAt(b, off)
	me.onRead(n)
	return
}
