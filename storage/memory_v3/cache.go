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
	Type int
	mu   sync.Mutex

	running   bool
	capacity  int64
	filled    int64
	readahead int64
	policy    Policy

	pieceCount    int
	pieceLength   int64
	piecePriority []int
	pieces        map[key]*Piece
	items         map[key]itemState

	closing chan struct{}

	bufferSize int
	buffers    [][]byte
	positions  []*BufferPosition
}

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

type itemState struct {
	Accessed time.Time
	Size     int64
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

func (c *Cache) GetReadaheadSize() int64 {
	return c.readahead
}

func (c *Cache) SetReadaheadSize(size int64) {
	c.readahead = size
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

	return c.pieces[key(m.Index())]
}

// Init creates buffers and underlying maps
func (c *Cache) Init(info *metainfo.Info) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[key]itemState)
	c.policy = new(lru)

	c.pieceCount = info.NumPieces()
	c.pieceLength = info.PieceLength
	c.piecePriority = make([]int, c.pieceCount)

	// Using max possible buffers + 2
	c.bufferSize = int(math.Ceil(float64(c.capacity)/float64(c.pieceLength)) + 2)
	if c.bufferSize > c.pieceCount {
		c.bufferSize = c.pieceCount
	}
	// c.readahead = int64((c.bufferSize/2)-1) * c.pieceLength
	c.readahead = int64(float64(c.capacity) * 0.4)

	c.buffers = make([][]byte, c.bufferSize)
	c.positions = make([]*BufferPosition, c.bufferSize)
	c.pieces = map[key]*Piece{}

	for i := 0; i < c.pieceCount; i++ {
		c.pieces[key(i)] = &Piece{
			c:        c,
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

// Close proxies Close from storage engine
func (c *Cache) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.running {
		return nil
	}

	c.running = false
	c.Stop()

	return nil
}

func (c *Cache) RemovePiece(idx int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	k := key(idx)
	if _, ok := c.pieces[k]; ok {
		c.remove(k)
	}
	// if idx < len(c.pieces) && c.pieces[idx].Position != -1 {
	// 	go func() {
	// 		delay := time.NewTicker(150 * time.Millisecond)
	// 		defer delay.Stop()
	//
	// 		for {
	// 			select {
	// 			case <-delay.C:
	// 				c.remove(idx)
	// 				return
	// 			}
	// 		}
	// 	}()
	// }
}

func (c *Cache) SyncPieces(active map[int]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// for _, v := range c.positions {
	// 	if _, ok := active[v.Index]; v.Used && !ok {
	// 		c.remove(v.Key)
	// 	}
	// }
}

// Start is watching Cache statistics
func (c *Cache) Start() {
	log.Debugf("StorageStart")

	c.running = true
	c.closing = make(chan struct{}, 1)
	progressTicker := time.NewTicker(1 * time.Second)

	defer progressTicker.Stop()
	defer close(c.closing)

	for {
		select {
		case <-progressTicker.C:
			// for k := range c.items {
			// 	log.Debugf("Item: %#v -- %#v", k, c.items[k].Accessed.String())
			// }

			info := c.Info()
			log.Debugf("Cap: %d | Size: %d | Items: %d | Capacity: %d \n", info.Capacity, info.Filled, info.Items, c.bufferSize)

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

	log.Debugf("Removing element: %#v", pi)

	if c.pieces[pi].Position != -1 {
		c.positions[c.pieces[pi].Position].Used = false
	}

	c.pieces[pi].Chunks.Clear()
	c.pieces[pi].Position = -1
	c.pieces[pi].Completed = false
	c.pieces[pi].Active = false
	c.pieces[pi].Size = 0

	c.updateItem(c.pieces[pi].Key, func(*itemState, bool) bool {
		return false
	})
}

func (c *Cache) updateItem(k key, u func(*itemState, bool) bool) {
	ii, ok := c.items[k]
	c.filled -= ii.Size
	if u(&ii, ok) {
		c.filled += ii.Size
		if int(k) != 0 {
			c.policy.Used(k, ii.Accessed)
		}
		c.items[k] = ii
	} else {
		log.Debugf("Forgetting: %#v", k)
		c.policy.Forget(k)
		delete(c.items, k)
	}
	c.trimToCapacity()
}

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
		log.Debugf("Trimming: %#v to %#v, %#v to %#v", c.filled, c.capacity, len(c.items), c.bufferSize)
		c.remove(c.policy.Choose().(key))
	}
	// for c.filled+c.pieceLength > c.capacity {
	// 	log.Debugf("Trimming: %#v to %#v", c.filled, c.capacity)
	// 	c.remove(c.policy.Choose().(key))
	// }
}
