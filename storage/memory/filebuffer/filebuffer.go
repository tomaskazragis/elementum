package filebuffer

import (
	"bytes"
	"errors"
	"io"
	"math"
	"sync"
	"time"

	"github.com/op/go-logging"
)

var log = logging.MustGetLogger("memory")

// CHUNK_SIZE Size of Chunk, comes from anacrolix/torrent
const CHUNK_SIZE = 1024 * 16

// Buffer implements interfaces implemented by files.
// The main purpose of this type is to have an in memory replacement for a
// file.
type Buffer struct {
	// Slice of []byte to store chunks
	Chunks [][]byte
	// Index indicates where in the buffer we are at
	Index    int64

	Accessed time.Time
	Modified time.Time

	mu         sync.Mutex
}

// New returns a new populated Buffer
func New(b []byte) *Buffer {
	return &Buffer{
		Chunks: [][]byte{},
		Accessed: time.Now(),
	}
}

func (f *Buffer) Len() (i int) {
	for _, b := range f.Chunks {
		i += len(b)
	}

	return
}

// Bytes returns the bytes available until the end of the buffer.
func (f *Buffer) Bytes() []byte {
	// if f.Index >= int64(f.Len()) {
	// 	return []byte{}
	// }
	buf := []byte{}
	for _, b := range f.Chunks {
		buf = append(buf, b...)
	}

	// return buf[f.Index:]
	return buf
}

// String implements the Stringer interface
func (f *Buffer) String() string {
	return string(f.Bytes()[f.Index:])
}

// Read implements io.Reader https://golang.org/pkg/io/#Reader
// Read reads up to len(p) bytes into p. It returns the number of bytes read (0 <= n <= len(p))
// and any error encountered. Even if Read returns n < len(p), it may use all of p as scratch
// space during the call. If some data is available but not len(p) bytes, Read conventionally
// returns what is available instead of waiting for more.

// When Read encounters an error or end-of-file condition after successfully reading n > 0 bytes,
// it returns the number of bytes read. It may return the (non-nil) error from the same call or
// return the error (and n == 0) from a subsequent call. An instance of this general case is
// that a Reader returning a non-zero number of bytes at the end of the input stream may return
// either err == EOF or err == nil. The next Read should return 0, EOF.
func (f *Buffer) Read(b []byte) (n int, err error) {
	if len(b) == 0 {
		return 0, nil
	}
	if f.Index >= int64(f.Len()) {
		return 0, io.EOF
	}

	f.Accessed = time.Now()

	n, err = bytes.NewBuffer(f.Bytes()[f.Index:]).Read(b)
	f.Index += int64(n)

	return n, err
}

func (f *Buffer) ReadAt(p []byte, off int64) (n int, err error) {
	if off < 0 {
		return 0, errors.New("filebuffer.ReadAt: negative offset")
	}

	f.Accessed = time.Now()

	reqLen := len(p)
	startIndex, startMargin := f.ChunkIndexForOffset(off)
	//lastIndex  := startIndex + int(math.Ceil(float64(reqLen - 1) / float64(CHUNK_SIZE))) - 1
	lastIndex, _ := f.ChunkIndexForOffset(off + int64(reqLen - CHUNK_SIZE))
	if lastIndex < startIndex {
		lastIndex = startIndex
	}
	// log.Debugf("R0: %#v, %#v, %#v, %#v, %#v, %#v", reqLen, startIndex, lastIndex, off, len(p), cap(p))

	for i := startIndex; i <= lastIndex; i++ {
		// if len(f.Chunks[i]) < CHUNK_SIZE {
		// 	return 0, io.EOF
		// }

		index := CHUNK_SIZE * (i - startIndex)
		// log.Debugf("R1: %#v, %#v, %#v, %#v, %#v, %#v", index, len(f.Chunks), i, len(p), cap(p), f.Chunks[i])
		// log.Debugf("R2: %#v, %#v, %#v, %#v, %#v", index, len(f.Chunks), len(f.Chunks[i]), cap(f.Chunks[i]), n)
		if len(f.Chunks[i]) == 0 || len(f.Chunks[i]) < startMargin {
			return 0, io.EOF
		}

		if i == startIndex && startMargin != 0 {
			// log.Debugf("Margin: %#v, %#v, %#v, %#v, %#v", startIndex, startMargin, off, len(f.Chunks[i]), cap(f.Chunks[i]))
			n += copy(p[index:], f.Chunks[i][startMargin:])
		} else {
			n += copy(p[index:], f.Chunks[i])
		}
		// log.Debugf("R2a: %#v, %#v, %#v, %#v, %#v", index, len(f.Chunks), len(f.Chunks[i]), cap(f.Chunks[i]), n)
	}
	// reqLen := len(p)
	// buffLen := int64(f.Len())
	// if off >= buffLen {
	// 	log.Debugf("Negative off: %#v, %#v, req: %#v", off, buffLen, reqLen)
	// 	return 0, io.EOF
	// }

	// n = copy(p, f.Bytes()[off:])
	if n < reqLen && lastIndex + 1 != len(f.Chunks) && n == 0 {
		log.Debugf("Less reqlen: %#v, req: %#v", n, reqLen)
		log.Debugf("R0: %#v, %#v, %#v, %#v != %#v, %#v, %#v", reqLen, startIndex, lastIndex, len(f.Chunks), off, len(p), cap(p))
		log.Debugf("R3: %#v, %#v, %#v, %#v", n, reqLen, cap(p), len(p))
		err = io.EOF
	}
	// log.Debugf("R3: %#v, %#v, %#v, %#v", n, reqLen, cap(p), len(p))
	return n, err
}

// Write implements io.Writer https://golang.org/pkg/io/#Writer
// by appending the passed bytes to the buffer unless the buffer is closed or index negative.
func (f *Buffer) Write(p []byte) (n int, err error) {
	log.Debugf("Write: %#v", len(p))
	if f.Index < 0 {
		return 0, io.EOF
	}

	f.Modified = time.Now()

	index := len(f.Chunks)
	f.CheckChunk(index)

	chunk := f.Chunks[index]
	copy(chunk[0:], p)

	n = len(p)
	f.Index += int64(n)

	return n, nil

	// we might have rewinded, let's reset the buffer before appending to it
	// idx := int(f.Index)
	// buffLen := f.Len()
	// if idx != buffLen && idx <= buffLen {
	// 	f.Buff = bytes.NewBuffer(f.Bytes()[:f.Index])
	// }
	// n, err = f.Buff.Write(p)

	// f.Index += int64(n)
	// return n, err
}

func (f *Buffer) WriteAt(p []byte, off int64) (n int, err error) {
	// if f.Index < 0 {
	// 	return 0, io.EOF
	// }

	// f.Modified = time.Now()

	// log.Debugf("WriteAt1 %#v -- %#v -- %#v", len(p), off, len(f.Chunks[f.GetChunkForOffset(off)]))
	index, _ := f.ChunkIndexForOffset(off)
	f.WriteChunk(index, p)
	// log.Debugf("WriteAt2 %#v -- %#v -- %#v", len(p), off, len(f.Chunks[f.GetChunkForOffset(off)]))
	// chunk := f.GetChunkForOffset(off)
	// copy(chunk[0:], p)

	// ioff := int(off)
	// iend := ioff + len(p)
	// log.Debugf("WriteAt1: cap: %#v, len:%#v Insert: end: %#v, off:%#v, len:%#v", cap(buf), len(buf), iend, ioff, len(p) )
	//
	// // if len(buf) < iend {
	// if cap(buf) < iend {
	// 	// if len(buf) == ioff {
	// 	// 	buf = append(buf, p...)
	// 	// 	return len(p), nil
	// 	// }
	// 	zero := make([]byte, 0, iend-cap(buf))
	// 	log.Debugf("WriteAt1a: cap: %#v, len:%#v Zero: cap: %#v, len:%#v", cap(buf), len(buf), cap(zero), len(zero) )
	// 	nbuf := make([]byte, len(buf), cap(buf) + cap(zero) + 100)
	// 	copy(nbuf, buf)
	// 	buf = nbuf
	// 	// buf = append(buf, zero...)
	// 	log.Debugf("WriteAt1b: cap: %#v, len:%#v Zero: cap: %#v, len:%#v", cap(buf), len(buf), cap(zero), len(zero) )
	//
	// 	// a := make([]byte, 0, 100)
	// 	// log.Debugf("WriteAt2a: cap: %#v, len:%#v Zero: cap: %#v, len:%#v", cap(a), len(a), cap(zero), len(zero) )
	// 	// a = append(a, zero...)
	// 	// log.Debugf("WriteAt2b: cap: %#v, len:%#v Zero: cap: %#v, len:%#v", cap(a), len(a), cap(zero), len(zero) )
	// }
	//
	// log.Debugf("WriteAt2: cap: %#v, len:%#v Insert: end: %#v, off:%#v, len:%#v", cap(buf), len(buf), iend, ioff, len(p) )
	// buf = append(buf[:ioff], append(p, buf[ioff:]...)...)
	// // buf = append([]byte{}, buf[0:ioff]..., p..., buf[iend:]...)
	// //copy(buf[ioff:ioff+cap(p)], p[:])
	// // buf = append(buf[ioff:], p)
	// log.Debugf("WriteAt3: cap: %#v, len:%#v Insert: end: %#v, off:%#v, len:%#v", cap(buf), len(buf), iend, ioff, len(p) )
	// f.Buff = bytes.NewBuffer(buf)
	// log.Debugf("WriteAt4: len:%#v Insert: off:%#v, len:%#v", len(buf), ioff, len(p) )
	return len(p), nil

	// ioff := int(off)
	// iend := ioff + len(p)
	// if f.Buff.Len() < iend {
	// 	if f.Buff.Len() == ioff {
	// 		//p.b = append(p.b, buf...)
	// 		n, err = f.Buff.Write(p)
	// 		return n, err
	// 	}
	//
	// 	zero := make([]byte, iend-len(f.Bytes()))
	// 	f.Buff = bytes.NewBuffer(f.Bytes()[:f.Index])
	// 	p.b = append(p.b, zero...)
	// }
	//
	// f.Buff = bytes.NewBuffer(f.Bytes()[ioff:])
	// f.Buff.Write(p)
	//
	// return len(p), nil
}

// Seek implements io.Seeker https://golang.org/pkg/io/#Seeker
func (f *Buffer) Seek(offset int64, whence int) (idx int64, err error) {
	f.Accessed = time.Now()

	var abs int64
	switch whence {
	case 0:
		abs = offset
	case 1:
		abs = int64(f.Index) + offset
	case 2:
		abs = int64(f.Len()) + offset
	default:
		return 0, errors.New("filebuffer.Seek: invalid whence")
	}
	if abs < 0 {
		return 0, errors.New("filebuffer.Seek: negative position")
	}
	f.Index = abs
	return abs, nil
}

func (f *Buffer) ChunkIndexForOffset(off int64) (index int, margin int) {
	// log.Debugf("Detect chunk: %#v -- %#v", off, int(off / int64(CHUNK_SIZE)))
	// index = int(math.Ceil(float64(off - 1) / float64(CHUNK_SIZE)))
	// margin = off - (CHUNK_SIZE * index)
	index = int(math.Ceil(float64(off) / float64(CHUNK_SIZE)))
	margin = int(math.Mod(float64(off), float64(CHUNK_SIZE)))
	if margin > 0 {
		index--
	}

	f.CheckChunk(index)

	return
}

func (f *Buffer) CheckChunk(index int) {
	// log.Debugf("Getting Chunk for index: %d", index)
	f.CreateChunks(index)
}

func (f *Buffer) CreateChunks(index int) {
	if index < len(f.Chunks) {
		return
	}

	// log.Debugf("Initiating chunks till: %d, current: %d", index, len(f.Chunks))

	t := make([][]byte, index + 1, index + 1)
	copy(t, f.Chunks)
	f.Chunks = t

	// log.Debugf("Prepared chunks till: %d, current: %d", index, len(f.Chunks))
	//
	for i := 0; i <= index; i++ {
		// log.Debugf("Chunk type (%d): %#v", i, f.Chunks[i])
		if f.Chunks[i] == nil {
			// log.Debugf("Making chunk (%d): %#v", i, f.Chunks[i])
			//f.Chunks[i] = make([]byte, 0, CHUNK_SIZE)
			f.Chunks[i] = make([]byte, 0, CHUNK_SIZE)
		}
	}
}

func (f *Buffer) WriteChunk(index int, p []byte) (n int, err error) {

	f.CheckChunk(index)

	// log.Debugf("WriteChunk1: %#v, %#v != %#v, %#v != %#v", index, cap(p), len(p), cap(f.Chunks[index]), len(f.Chunks[index]))

	if len(p) != len(f.Chunks[index]) {
		// log.Debugf("WriteChunk1 resize: %#v, %#v != %#v, %#v != %#v", index, cap(p), len(p), cap(f.Chunks[index]), len(f.Chunks[index]))
		f.Chunks[index] = make([]byte, len(p))
	}
	// log.Debugf("Write 2: %#v != %#v", len(p), len(chunk))

	// // ary := f.Chunks[index]
	// ary := make([]byte, len(p))
	// copy(ary, p)
	copy(f.Chunks[index], p)

	// log.Debugf("Write 3: %#v copy %#v", len(p), len(chunk))
	// log.Debugf("WriteChunk2: %#v, %#v != %#v, %#v != %#v", index, cap(p), len(p), cap(f.Chunks[index]), len(f.Chunks[index]))
	return len(f.Chunks[index]), nil
}

// Close implements io.Closer https://golang.org/pkg/io/#Closer
// It closes the buffer, rendering it unusable for I/O. It returns an error, if any.
func (f *Buffer) Close() error {
	// f.isClosed = true
	return nil
}
