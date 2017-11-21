package cache

import (
	"errors"
	"math"
	"os"
	"path"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	// "github.com/anacrolix/missinggo/filecache"
	"github.com/anacrolix/missinggo/resource"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"

	"github.com/RoaringBitmap/roaring"
	"github.com/dustin/go-humanize"
	"github.com/op/go-logging"
	// "github.com/elgatito/elementum/storage/memory/filebuffer"
)

var log = logging.MustGetLogger("memory")

// Cache main object
type Cache struct {
	s  storage.ClientImpl
	mu sync.Mutex

	capacity int64
	filled   int64
	policy   Policy

	progressTicker *time.Ticker
	closing        chan struct{}

	items  map[key]itemState
	pieces map[key]*Piece

	buffers   [][]byte
	positions []bool
}

// Piece stores meta information about buffer contents
type Piece struct {
	Path     string
	Position int
	Active   bool
	Size     int64
	Accessed time.Time
	Chunks   *roaring.Bitmap
	mu       sync.Mutex
}

// type MemoryCache struct {
// 	mu        sync.Mutex
// 	buffering *bool
// 	capacity  int64
// 	filled    int64
// 	policy    Policy
// 	items     map[key]itemState
// 	// buffers   map[key]*filebuffer.Buffer
// }

// CacheInfo is a container for basic active Cache into
type CacheInfo struct {
	Capacity int64
	Filled   int64
	NumItems int
}

// ItemInfo contains basic information about the piece
type ItemInfo struct {
	Path     key
	Accessed time.Time
	Size     int64
}

type itemState struct {
	Accessed time.Time
	Size     int64
}

// Chunk of a piece, to get or give
// type Chunk struct {
// 	piece  string
// 	chunk  []byte
// 	offset int64
// }

// NewMemoryStorage initializer function
func NewMemoryStorage(maxMemorySize int64) *Cache {
	log.Debugf("Memory: %#v", maxMemorySize)
	c := &Cache{}

	c.SetCapacity(maxMemorySize)
	c.SetStorage(storage.NewResourcePieces(c.AsResourceProvider()))

	return c
}

// SetStorage changes underlying ClientImpl
func (c *Cache) SetStorage(s storage.ClientImpl) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.s = s
}

// SetCapacity for cache
func (c *Cache) SetCapacity(capacity int64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	log.Debugf("Setting max memory size to %#v bytes", capacity)
	c.capacity = capacity
}

// OpenTorrent proxies OpenTorrent from storage to prepare buffers for storing chunks
func (c *Cache) OpenTorrent(info *metainfo.Info, infoHash metainfo.Hash) (storage.TorrentImpl, error) {
	c.Init(info)
	go c.Start()

	log.Debugf("OpenTorrent: %#v", c.s)
	return c.s.OpenTorrent(info, infoHash)
}

// Init creates buffers and underlying maps
func (c *Cache) Init(info *metainfo.Info) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.filled = 0
	c.policy = new(lru)
	c.items = make(map[key]itemState)

	size := int64(math.Ceil(float64(c.capacity)/float64(info.PieceLength))) + 1
	if size > int64(info.NumPieces()) {
		size = int64(info.NumPieces())
	}

	c.pieces = map[key]*Piece{}
	c.buffers = make([][]byte, size)
	c.positions = make([]bool, size)

	// for i := 0; i < info.NumPieces(); i++ {
	// 	log.Debugf("Piece: %#v === %#v === %#v", info.Piece(i).Hash(), info.Piece(i).Hash().AsString(), info.Piece(i).Hash().String())
	// 	c.pieces[ key(info.Piece(i).Hash().AsString()) ] = &Piece{ Position: -1 }
	// }

	for i := range c.buffers {
		c.buffers[i] = make([]byte, info.PieceLength)
		// c.info[i].Chunks = roaring.NewBitmap()
	}

}

// Info returns information for Cache
func (c *Cache) Info() (ret CacheInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()

	ret.Capacity = c.capacity
	ret.Filled = c.filled
	ret.NumItems = len(c.items)
	return
}

// Close proxies Close from storage engine
func (c *Cache) Close() error {
	c.closing <- struct{}{}
	c.Stop()

	return c.s.Close()
}

// Seek proxies seek event from Kodi
func (c *Cache) Seek() {
	log.Debugf("StorageSeek")
}

// Start is watching Cache statistics
func (c *Cache) Start() {
	log.Debugf("StorageStart")

	c.closing = make(chan struct{}, 1)
	c.progressTicker = time.NewTicker(1 * time.Second)

	defer c.progressTicker.Stop()
	defer close(c.closing)

	for {
		select {
		case <-c.progressTicker.C:
			info := c.Info()
			log.Debugf("Cap: %d | Size: %d | Items: %d \n", info.Capacity, info.Filled, info.NumItems)

			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			log.Debugf("Memory: %s, %s, %s, %s", humanize.Bytes(m.HeapSys), humanize.Bytes(m.HeapAlloc), humanize.Bytes(m.HeapIdle), humanize.Bytes(m.HeapReleased))

		case <-c.closing:
			return

		}
	}
}

// Stop ends progress timers, removes buffers, free memory to OS
func (c *Cache) Stop() {
	log.Debugf("StorageStop")

	c.mu.Lock()
	defer c.mu.Unlock()

	c.closing <- struct{}{}

	c.buffers = nil
	c.pieces = map[key]*Piece{}

	debug.FreeOSMemory()
}

// type MemoryCache struct {
// 	mu        sync.Mutex
// 	buffering *bool
// 	capacity  int64
// 	filled    int64
// 	policy    Policy
// 	items     map[key]itemState
// 	// buffers   map[key]*filebuffer.Buffer
// }
//
// type MemoryCacheInfo struct {
// 	Capacity int64
// 	Filled   int64
// 	NumItems int
// }
//
// type ItemInfo struct {
// 	Path     key
// 	Accessed time.Time
// 	Size     int64
// }
//
// type itemState struct {
// 	Accessed time.Time
// 	Size     int64
// }

// func (i *itemState) FromBufferInfo(buf *filebuffer.Buffer) {
// 	i.Size = int64(buf.Len())
// 	i.Accessed = buf.Accessed
// 	if buf.Modified.After(i.Accessed) {
// 		i.Accessed = buf.Modified
// 	}
// }

// NewMemoryCache Create storage
// func NewMemoryCache() (ret *MemoryCache, err error) {
// 	ret = &MemoryCache{
// 		capacity: -1, // unlimited
// 		// buffers:  map[key]*filebuffer.Buffer{},
// 	}
//
// 	ret.mu.Lock()
// 	go func() {
// 		defer ret.mu.Unlock()
// 		// ret.rescan()
// 	}()
// 	return
// }

// Clear the memory engine entirely
// func (me *MemoryCache) Clear() {
// 	log.Debugf("Cleaning up buffers")
// 	// me.buffers = map[key]*filebuffer.Buffer{}
// 	me.items = map[key]itemState{}
// 	// runtime.GC()
// }

// OpenBuffer gets buffer for specific piece
func (c *Cache) OpenBuffer(path string, iswrite bool) (ret *File, err error) {
	key := sanitizePath(path)

	c.mu.Lock()
	defer c.mu.Unlock()

	i, ok := c.pieces[key]
	if !ok {
		if iswrite {
			c.pieces[key] = &Piece{
				Path:     path,
				Position: -1,
				Chunks:   roaring.NewBitmap(),
				Accessed: time.Now(),
			}
			i = c.pieces[key]
		} else {
			return nil, errors.New("Not found in pieces")
		}

		// if iswrite {
		// 	c.pieces[key] = &Piece{
		// 		Path: path,
		// 		Position: -1,
		// 		Chunks: roaring.NewBitmap(),
		// 		Accessed: time.Now(),
		// 	}
		// 	i = c.pieces[key]
		// } else {
		// 	return nil, errors.New("Not exists")
		// }
	}

	if i != nil && i.Position == -1 && iswrite {
		// c.mu.Lock()
		// defer c.mu.Unlock()

		// log.Debugf("Positions: %#v", c.positions)
		for index, v := range c.positions {
			// log.Debugf("  ... in piece: %#v", c.info[i])
			if v {
				continue
			}

			c.positions[index] = true

			// c.pieces[k].Chunks = roaring.NewBitmap()
			c.pieces[key].Position = index
			c.pieces[key].Active = true
			c.pieces[key].Size = 0
			c.pieces[key].Accessed = time.Now()

			break
		}
	}

	// c.mu.Unlock()

	// log.Debugf("Open: %#v : %#v : %#v ", path, key, i)

	ret = &File{
		c:        c,
		position: i.Position,
		key:      key,
		path:     path,

		onRead: func(n int) {
			// c.mu.Lock()
			// defer c.mu.Unlock()

			c.updateItem(key, func(i *itemState, ok bool) bool {
				i.Accessed = time.Now()
				return ok
			})
		},
		afterWrite: func(endOff int64) {
			c.mu.Lock()
			defer c.mu.Unlock()

			c.updateItem(key, func(i *itemState, ok bool) bool {
				i.Accessed = time.Now()

				// log.Debugf("After: %#v -- %#v", endOff, i.Size)
				if endOff > i.Size {
					i.Size = endOff
				}
				return ok
			})
		},
	}

	// c.mu.Lock()
	// defer c.mu.Unlock()
	c.updateItem(key, func(i *itemState, ok bool) bool {
		if !ok {
			*i, ok = c.statKey(key)
		}
		i.Accessed = time.Now()
		return ok
	})

	return
}

func (c *Cache) statKey(k key) (i itemState, ret bool) {
	if v, ok := c.pieces[k]; ok {
		i.Size = v.Size
		i.Accessed = v.Accessed
	} else {
		log.Debugf("Key not found: %#v", k)
	}

	ret = true
	return
}

func (c *Cache) updateItem(k key, u func(*itemState, bool) bool) {
	ii, ok := c.items[k]
	c.filled -= ii.Size
	if u(&ii, ok) {
		c.filled += ii.Size
		c.policy.Used(k, ii.Accessed)
		c.items[k] = ii
	} else {
		c.policy.Forget(k)
		delete(c.items, k)
		ii.Size = 0
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
	for c.filled > c.capacity {
		// log.Debugf("Removing for filled: %#v, cap: %#v", c.filled, c.capacity)
		c.remove(c.policy.Choose().(key))
	}
}

func (c *Cache) Remove(path string) error {
	// log.Debugf("Super remove called: %#v", path)
	c.mu.Lock()
	defer c.mu.Unlock()

	key := sanitizePath(path)
	if c.pieces[key].Path != path {
		return nil
	}

	return c.remove(key)
}

func (c *Cache) remove(k key) error {
	//if c.align[k]
	// if _, ok := me.buffers[k]; !ok {
	// 	return errors.New("Not found")
	// }

	// debug.PrintStack()
	// log.Debugf("Removing element: %#v", k)
	if p, ok := c.pieces[k]; !ok {
		return nil
	} else if p.Position != -1 {
		c.positions[c.pieces[k].Position] = false
	}

	c.pieces[k].Chunks.Clear()
	c.pieces[k].Position = -1
	// c.pieces[k].Size = 0

	// delete(c.align, k)
	// me.buffers[k] = nil
	// delete(me.buffers, k)
	// runtime.GC()

	c.updateItem(k, func(*itemState, bool) bool {
		return false
	})
	return nil
}

func (c *Cache) Stat(path string) (os.FileInfo, error) {
	f, err := c.OpenBuffer(path, false)
	if err != nil {
		return nil, err
	}
	// else if i, ok := c.pieces[f.key]; ok && !i.Active {
	// 	return nil, errors.New("Not active")
	// }

	return f.Stat()
}

func (c *Cache) AsResourceProvider() resource.Provider {
	return &uniformResourceProvider{c}
}

// An empty return path is an error.
func sanitizePath(p string) (ret key) {
	if p == "" {
		return
	}
	if i := strings.Index(p, "/"); i >= 0 {
		p = p[i+1:]
	}
	ret = key(path.Clean("/" + p))
	if ret[0] == '/' {
		ret = ret[1:]
	}

	return
}
