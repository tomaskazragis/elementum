package memory

import (
	// "errors"
	// "fmt"
	// "os"
	// "path"
	// "runtime"
	// "strings"
	"math"
	"runtime/debug"
	"sync"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"

	"github.com/RoaringBitmap/roaring"
	"github.com/op/go-logging"
)

var log = logging.MustGetLogger("memory")

// Cache main object
type Cache struct {
	mu sync.Mutex

	capacity int64

	pieceCount    int
	pieceLength   int64
	piecePriority []int
	pieces        []*Piece

	closing chan struct{}

	buffers   [][]byte
	positions []*BufferPosition
}

type BufferPosition struct {
	Used  bool
	Index int
}

// CacheInfo is a container for basic active Cache into
type CacheInfo struct {
	Capacity int64
	Filled   int64
	Items    int64
}

// NewMemoryStorage initializer function
func NewMemoryStorage(maxMemorySize int64) *Cache {
	log.Debugf("Memory: %#v", maxMemorySize)
	c := &Cache{}

	c.SetCapacity(maxMemorySize)

	return c
}

// SetCapacity for cache
func (c *Cache) SetCapacity(capacity int64) {
	// c.mu.Lock()
	// defer c.mu.Unlock()

	log.Debugf("Setting max memory size to %#v bytes", capacity)
	c.capacity = capacity
}

// OpenTorrent proxies OpenTorrent from storage to prepare buffers for storing chunks
func (c *Cache) OpenTorrent(info *metainfo.Info, infoHash metainfo.Hash) (storage.TorrentImpl, error) {
	c.Init(info)
	go c.Start()

	return c, nil
}

func (c *Cache) Piece(m metainfo.Piece) storage.PieceImpl {
	c.mu.Lock()
	defer c.mu.Unlock()

	if m.Index() >= len(c.pieces) {
		return nil
	}

	return c.pieces[m.Index()]
}

// Init creates buffers and underlying maps
func (c *Cache) Init(info *metainfo.Info) {
	// c.mu.Lock()
	// defer c.mu.Unlock()

	c.pieceCount = info.NumPieces()
	c.pieceLength = info.PieceLength
	c.piecePriority = make([]int, c.pieceCount)

	// Using max possible buffers + 2
	size := int64(math.Ceil(float64(c.capacity)/float64(c.pieceLength))) + 2
	if size > int64(c.pieceCount) {
		size = int64(c.pieceCount)
	}

	c.buffers = make([][]byte, size)
	c.positions = make([]*BufferPosition, size)
	c.pieces = make([]*Piece, c.pieceCount)

	for i := 0; i < c.pieceCount; i++ {
		c.pieces[i] = &Piece{
			c:        c,
			Position: -1,
			Index:    i,
			Length:   info.Piece(i).Length(),
			Hash:     info.Piece(i).Hash().HexString(),
			Chunks:   roaring.NewBitmap(),
		}
	}

	for i := range c.buffers {
		c.buffers[i] = make([]byte, c.pieceLength)
		c.positions[i] = &BufferPosition{}
	}
}

// Info returns information for Cache
func (c *Cache) Info() (ret CacheInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var items, filled int64
	for _, v := range c.positions {
		if v.Used {
			items++
			filled += c.pieces[v.Index].Size
		}
	}

	ret.Capacity = c.capacity
	ret.Filled = filled
	ret.Items = items
	return
}

// Close proxies Close from storage engine
func (c *Cache) Close() error {
	c.Stop()

	return nil
}

func (c *Cache) RemovePiece(idx int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if idx < len(c.pieces) && c.pieces[idx].Position != -1 {
		go func() {
			delay := time.NewTicker(100 * time.Millisecond)
			defer delay.Stop()

			for {
				select {
				case <-delay.C:
					c.remove(idx)
					return
				}
			}
		}()

		// c.remove(idx)
	}
}

func (c *Cache) SyncPieces(active map[int]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, v := range c.positions {
		if _, ok := active[v.Index]; v.Used && !ok {
			c.remove(v.Index)
		}
	}
}

// Start is watching Cache statistics
func (c *Cache) Start() {
	log.Debugf("StorageStart")

	c.closing = make(chan struct{}, 1)
	progressTicker := time.NewTicker(1 * time.Second)

	defer progressTicker.Stop()
	defer close(c.closing)

	for {
		select {
		case <-progressTicker.C:
			info := c.Info()
			log.Debugf("Cap: %d | Size: %d | Items: %d \n", info.Capacity, info.Filled, info.Items)

			// str := ""
			// for i := 0; i < 30; i++ {
			// 	str += fmt.Sprintf(" %d:%v", i, c.pieces[i].Position)
			// }
			// log.Debugf("Pieces: %s", str)
			// var m runtime.MemStats
			// runtime.ReadMemStats(&m)
			// log.Debugf("Memory: %s, %s, %s, %s", humanize.Bytes(m.HeapSys), humanize.Bytes(m.HeapAlloc), humanize.Bytes(m.HeapIdle), humanize.Bytes(m.HeapReleased))

		case <-c.closing:
			return

		}
	}
}

// Stop ends progress timers, removes buffers, free memory to OS
func (c *Cache) Stop() {
	log.Debugf("StorageStop")

	// c.mu.Lock()
	// defer c.mu.Unlock()

	c.closing <- struct{}{}

	c.buffers = nil
	c.pieces = nil
	c.positions = nil
	// c.piecesIdx = map[key]int{}

	debug.FreeOSMemory()
}

// func (c *Cache) ReadAt(pi int, b []byte, off int64) (int, error) {
// 	buf, err := c.OpenBuffer(pi, false)
// 	if err != nil {
// 		return 0, err
// 	}
//
// 	return buf.ReadAt(b, off)
// }
//
// func (c *Cache) WriteAt(pi int, b []byte, off int64) (n int, err error) {
// 	buf, err := c.OpenBuffer(pi, true)
// 	if err != nil {
// 		return 0, err
// 	}
//
// 	return buf.WriteAt(b, off)
// }

// func (c *Cache) OpenBuffer(pi int, iswrite bool) (ret *Buffer, err error) {
// 	c.mu.Lock()
// 	defer c.mu.Unlock()
//
// 	if pi >= len(c.pieces) {
// 		return nil, errors.New("Piece index not valid")
// 	}
//
// 	if !c.pieces[pi].Active && iswrite {
// 		for index, v := range c.positions {
// 			if v.Used {
// 				continue
// 			}
//
// 			v.Used = true
// 			v.Index = pi
//
// 			c.pieces[pi].Position = index
// 			c.pieces[pi].Active = true
// 			c.pieces[pi].Size = 0
// 			c.pieces[pi].Modified = time.Now()
//
// 			break
// 		}
//
// 		if !c.pieces[pi].Active {
// 			log.Debugf("Buffer not assigned: %#v", c.positions)
// 			return nil, errors.New("Could not assign buffer")
// 		}
// 	}
//
// 	ret = &Buffer{
// 		c: c,
// 		p: c.pieces[pi],
// 	}
//
// 	return
// }

func (c *Cache) remove(pi int) {
	// Don't allow to delete first piece, it's used everywhere
	if pi == 0 {
		return
	}

	log.Debugf("Removing element: %#v", pi)

	if c.pieces[pi].Position != -1 {
		c.positions[c.pieces[pi].Position].Used = false
	}

	c.pieces[pi].Chunks.Clear()
	c.pieces[pi].Position = -1
	c.pieces[pi].Completed = false
	c.pieces[pi].Active = false
	c.pieces[pi].Size = 0
}
