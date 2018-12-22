package memory

import (
	// "errors"
	// "os"
	// "path"
	// "runtime"
	// "strings"
	// "fmt"
	"math"
	"runtime/debug"
	"sync"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	humanize "github.com/dustin/go-humanize"

	"github.com/RoaringBitmap/roaring"

	estorage "github.com/elgatito/elementum/storage"
)

// Cache ...
type Cache struct {
	s  *Storage
	mu *sync.Mutex

	id        string
	running   bool
	capacity  int64
	filled    int64
	readahead int64

	policy Policy

	pieceCount    int
	pieceLength   int64
	piecePriority []int
	pieces        map[key]*Piece
	items         map[key]ItemState

	closing chan struct{}

	bufferSize int
	buffers    [][]byte
	positions  []*BufferPosition
}

// BufferItem ...
// type BufferItem struct {
// 	mu   *sync.Mutex
// 	body []byte
// }

// BufferPosition ...
type BufferPosition struct {
	Used  bool
	Index int
	Key   key
}

// CacheInfo is a container for basic active Cache into
type CacheInfo struct {
	Capacity int64
	Filled   int64
	Items    int
}

// ItemState ...
type ItemState struct {
	Accessed time.Time
	Size     int64
}

// SetCapacity ...
func (c *Cache) SetCapacity(capacity int64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	log.Infof("Setting max memory size to %#v bytes", capacity)
	c.capacity = capacity
}

// GetReadaheadSize ...
func (c *Cache) GetReadaheadSize() int64 {
	return c.readahead
}

// SetReadaheadSize ...
func (c *Cache) SetReadaheadSize(size int64) {
	log.Debugf("Setting readahead size to: %s", humanize.Bytes(uint64(size)))

	c.readahead = size
}

// GetTorrentStorage ...
func (c *Cache) GetTorrentStorage(hash string) estorage.TorrentStorage {
	return nil
}

// OpenTorrent ...
func (c *Cache) OpenTorrent(info *metainfo.Info, infoHash metainfo.Hash) (storage.TorrentImpl, error) {
	return nil, nil
}

// Piece ...
func (c *Cache) Piece(m metainfo.Piece) storage.PieceImpl {
	c.mu.Lock()
	defer c.mu.Unlock()

	if m.Index() >= len(c.pieces) {
		return nil
	}

	return c.pieces[key(m.Index())]
}

// Init creates buffers and underlying maps
func (c *Cache) Init(info *metainfo.Info) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[key]ItemState)
	c.policy = new(lru)

	c.pieceCount = info.NumPieces()
	c.pieceLength = info.PieceLength
	c.piecePriority = make([]int, c.pieceCount)

	// Using max possible buffers + 2
	c.bufferSize = int(math.Ceil(float64(c.capacity)/float64(c.pieceLength)) + 2)
	if c.bufferSize > c.pieceCount {
		c.bufferSize = c.pieceCount
	}
	c.readahead = int64(float64(c.capacity) * readaheadRatio)

	c.buffers = make([][]byte, c.bufferSize)
	c.positions = make([]*BufferPosition, c.bufferSize)
	c.pieces = map[key]*Piece{}

	log.Infof("Init memory for torrent. Buffers: %d, Piece length: %s, Capacity: %s", c.bufferSize, humanize.Bytes(uint64(c.pieceLength)), humanize.Bytes(uint64(c.capacity)))

	for i := 0; i < c.pieceCount; i++ {
		c.pieces[key(i)] = &Piece{
			c:        c,
			mu:       &sync.Mutex{},
			Position: -1,
			Index:    i,
			Key:      key(i),
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

	ret.Capacity = c.capacity
	ret.Filled = c.filled
	ret.Items = len(c.items)
	return
}

// Close ...
func (c *Cache) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.s.items[c.id]; ok {
		c.s.mu.Lock()
		defer c.s.mu.Unlock()
		delete(c.s.items, c.id)
	}

	if !c.running {
		return nil
	}

	c.running = false
	c.Stop()

	return nil
}

// RemovePiece ...
func (c *Cache) RemovePiece(idx int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	k := key(idx)
	if _, ok := c.pieces[k]; ok {
		c.remove(k)
	}
}

// func (c *Cache) SyncPieces(active map[int]bool) {
// 	c.mu.Lock()
// 	defer c.mu.Unlock()
//
// 	// for _, v := range c.positions {
// 	// 	if _, ok := active[v.Index]; v.Used && !ok {
// 	// 		c.remove(v.Key)
// 	// 	}
// 	// }
// }

// Start is watching Cache statistics
func (c *Cache) Start() {
	log.Debugf("StorageStart")

	c.running = true
	c.closing = make(chan struct{}, 1)
	progressTicker := time.NewTicker(1 * time.Second)

	defer progressTicker.Stop()
	defer close(c.closing)

	// var lastFilled int64

	for {
		select {
		case <-progressTicker.C:
			// for k := range c.items {
			// 	log.Debugf("Item: %#v -- %#v", k, c.items[k].Accessed.String())
			// }

			// info := c.Info()
			// log.Debugf("Cap: %d | Size: %d | Items: %d | Capacity: %d \n", info.Capacity, info.Filled, info.Items, c.bufferSize)

			// if info.Filled == lastFilled {
			// log.Debugf("Download stale. Storage lock: %#v. Cache lock: %#v", c.s.mu, c.mu)
			// locks := ""
			// for i, b := range c.buffers {
			// 	locks += fmt.Sprintf("%#v:%#v | ", i, b.mu)
			// }
			// log.Debugf("Locks: %#v", locks)

			// positions := ""
			// for i, p := range c.positions {
			// 	pk := 0
			// 	if p.Index != 0 {
			// 		pk = c.pieces[p.Key].Position
			// 	}
			// 	positions += fmt.Sprintf("%#v:%#v-%#v | ", i, p.Index, pk)
			// }
			// log.Debugf("Positions: %#v", positions)
			// }

			// lastFilled = info.Filled

			// str := ""
			// for i := 0; i < 30; i++ {
			// 	str += fmt.Sprintf(" %d:%v", i, c.pieces[i].Position)
			// }
			// log.Debugf("Pieces: %s", str)
			// var m runtime.MemStats
			// runtime.ReadMemStats(&m)
			// log.Debugf("Memory: %s, %s, %s, %s", humanize.Bytes(m.HeapSys), humanize.Bytes(m.HeapAlloc), humanize.Bytes(m.HeapIdle), humanize.Bytes(m.HeapReleased))

		case <-c.closing:
			log.Debugf("Closing monitoring")
			c.running = false
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

	go func() {
		delay := time.NewTicker(1 * time.Second)
		defer delay.Stop()

		for {
			select {
			case <-delay.C:
				c.buffers = nil
				c.pieces = nil
				c.positions = nil

				debug.FreeOSMemory()

				return
			}
		}
	}()
}

func (c *Cache) remove(pi key) {
	// Don't allow to delete first piece, it's used everywhere
	if pi == 0 {
		return
	}

	// log.Debugf("Removing element: %#v", pi)

	if c.pieces[pi].Position != -1 {
		c.positions[c.pieces[pi].Position].Used = false
		c.positions[c.pieces[pi].Position].Index = 0
	}

	c.pieces[pi].Position = -1
	c.pieces[pi].Completed = false
	c.pieces[pi].Active = false
	c.pieces[pi].mu.Lock()
	c.pieces[pi].Reset()
	c.pieces[pi].mu.Unlock()

	c.updateItem(c.pieces[pi].Key, func(*ItemState, bool) bool {
		return false
	})
}

func (c *Cache) updateItem(k key, u func(*ItemState, bool) bool) {
	ii, ok := c.items[k]
	c.filled -= ii.Size
	if u(&ii, ok) {
		c.filled += ii.Size
		if int(k) != 0 {
			c.policy.Used(k, ii.Accessed)
		}
		c.items[k] = ii
	} else {
		// log.Debugf("Forgetting: %#v", k)
		c.policy.Forget(k)
		delete(c.items, k)
	}
	c.trimToCapacity()
}

// TrimToCapacity ...
func (c *Cache) TrimToCapacity() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.trimToCapacity()
}

func (c *Cache) trimToCapacity() {
	if c.capacity < 0 {
		return
	}
	for len(c.items) >= c.bufferSize {
		// for k := range c.items {
		// 	log.Debugf("Active: %#v -- %#v", k, c.items[k].Accessed.String())
		// }
		// log.Debugf("Trimming: %#v to %#v, %#v to %#v", c.filled, c.capacity, len(c.items), c.bufferSize)
		c.remove(c.policy.Choose().(key))
	}
	// for c.filled+c.pieceLength > c.capacity {
	// 	log.Debugf("Trimming: %#v to %#v", c.filled, c.capacity)
	// 	c.remove(c.policy.Choose().(key))
	// }
}
