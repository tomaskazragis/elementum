package memory

import (
	"errors"
	"os"
	"path"
	"runtime"
	"sync"
	"time"

	"github.com/anacrolix/missinggo/filecache"
	"github.com/anacrolix/missinggo/resource"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"

	"github.com/op/go-logging"
	"github.com/dustin/go-humanize"

	"github.com/elgatito/elementum/storage/memory/filebuffer"
)

var log = logging.MustGetLogger("memory")

type MemoryStorage struct {
	storage storage.ClientImpl
	cache   *MemoryCache

	closing        chan struct{}
	progressTicker *time.Ticker
}

// NewMemoryStorage initializer function
func NewMemoryStorage(maxMemorySize int64) *MemoryStorage {
	mc, err := NewMemoryCache()
	if err != nil {
		return nil
	}

	mc.SetCapacity(maxMemorySize)

	return &MemoryStorage{
		storage: storage.NewResourcePieces(mc.AsResourceProvider()),
		cache:   mc,
	}
}

func (ms *MemoryStorage) OpenTorrent(info *metainfo.Info, infoHash metainfo.Hash) (storage.TorrentImpl, error) {
	go ms.Start()
	return ms.storage.OpenTorrent(info, infoHash)
}

func (ms *MemoryStorage) Close() error {
	ms.Stop()
	ms.closing <- struct{}{}

	return ms.storage.Close()
}

func (ms *MemoryStorage) Seek() {
	log.Debugf("StorageSeek")
}

func (ms *MemoryStorage) Start() {
	log.Debugf("StorageStart")

	ms.closing = make(chan struct{}, 1)
	ms.progressTicker = time.NewTicker(1 * time.Second)

	defer ms.progressTicker.Stop()
	defer close(ms.closing)

	for {
		select {
		case <-ms.progressTicker.C:
			info := ms.cache.Info()
			log.Debugf("Cap: %d | Size: %d | Items: %d \n", info.Capacity, info.Filled, info.NumItems)

			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			log.Debugf("Memory: %s, %s, %s, %s", humanize.Bytes(m.HeapSys), humanize.Bytes(m.HeapAlloc), humanize.Bytes(m.HeapIdle), humanize.Bytes(m.HeapReleased))

		case <-ms.closing:
			return

		}
	}
}

func (ms *MemoryStorage) Stop() {
	log.Debugf("StorageStop")

	ms.closing <- struct{}{}
	ms.cache.Clear()
}



type MemoryCache struct {
	mu        sync.Mutex
	buffering *bool
	capacity  int64
	filled    int64
	policy    Policy
	items     map[key]itemState
	buffers   map[key]*filebuffer.Buffer
}

type MemoryCacheInfo struct {
	Capacity int64
	Filled   int64
	NumItems int
}

type ItemInfo struct {
	Path     key
	Accessed time.Time
	Size     int64
}

type itemState struct {
	Accessed time.Time
	Size     int64
}

func (i *itemState) FromBufferInfo(buf *filebuffer.Buffer) {
	i.Size = int64(buf.Len())
	i.Accessed = buf.Accessed
	if buf.Modified.After(i.Accessed) {
		i.Accessed = buf.Modified
	}
}

// NewMemoryCache Create storage
func NewMemoryCache() (ret *MemoryCache, err error) {
	ret = &MemoryCache{
		capacity: -1, // unlimited
		buffers:  map[key]*filebuffer.Buffer{},
	}

	ret.mu.Lock()
	go func() {
		defer ret.mu.Unlock()
		ret.rescan()
	}()
	return
}

// Clear the memory engine entirely
func (me *MemoryCache) Clear() {
	log.Debugf("Cleaning up buffers")
	me.buffers = map[key]*filebuffer.Buffer{}
	me.items = map[key]itemState{}
	runtime.GC()
}

// OpenBuffer gets buffer for specific piece
func (me *MemoryCache) OpenBuffer(path string) (ret *File, err error) {
	k := sanitizePath(path)

	me.mu.Lock()
	if _, ok := me.buffers[k]; !ok {
		me.buffers[k] = filebuffer.New([]byte{})
	}
	me.mu.Unlock()

	ret = &File{
		f:            me.buffers[k],
		path:         k,
		pathOriginal: path,
		onRead: func(n int) {
			me.mu.Lock()
			defer me.mu.Unlock()
			me.updateItem(k, func(i *itemState, ok bool) bool {
				i.Accessed = time.Now()
				return ok
			})
		},
		afterWrite: func(endOff int64) {
			me.mu.Lock()
			defer me.mu.Unlock()
			me.updateItem(k, func(i *itemState, ok bool) bool {
				i.Accessed = time.Now()
				if endOff > i.Size {
					i.Size = endOff
				}
				return ok
			})
		},
	}

	me.mu.Lock()
	defer me.mu.Unlock()
	me.updateItem(k, func(i *itemState, ok bool) bool {
		if !ok {
			*i, ok = me.statKey(k)
		}
		i.Accessed = time.Now()
		return ok
	})

	return
}

func (me *MemoryCache) rescan() {
	me.filled = 0
	me.policy = new(lru)
	me.items = make(map[key]itemState)
}

func (me *MemoryCache) statKey(k key) (i itemState, ok bool) {
	buf, ok := me.buffers[k]
	if !ok {
		return
	}
	i.FromBufferInfo(buf)
	ok = true
	return
}

func (me *MemoryCache) updateItem(k key, u func(*itemState, bool) bool) {
	ii, ok := me.items[k]
	me.filled -= ii.Size
	if u(&ii, ok) {
		me.filled += ii.Size
		me.policy.Used(k, ii.Accessed)
		me.items[k] = ii
	} else {
		me.policy.Forget(k)
		delete(me.items, k)
	}
	me.trimToCapacity()
}

func (me *MemoryCache) TrimToCapacity() {
	me.mu.Lock()
	defer me.mu.Unlock()
	me.trimToCapacity()
}

func (me *MemoryCache) trimToCapacity() {
	if me.capacity < 0 {
		return
	}
	for me.filled > me.capacity {
		// log.Debugf("Removing for filled: %#v, cap: %#v", me.filled, me.capacity)
		me.remove(me.policy.Choose().(key))
	}
}

func (me *MemoryCache) Remove(path string) error {
	me.mu.Lock()
	defer me.mu.Unlock()
	return me.remove(sanitizePath(path))
}

func (me *MemoryCache) remove(k key) error {
	if _, ok := me.buffers[k]; !ok {
		return errors.New("Not found")
	}

	// debug.PrintStack()
	log.Debugf("Removing element: %#v", k)
	me.buffers[k] = nil
	delete(me.buffers, k)
	runtime.GC()

	me.updateItem(k, func(*itemState, bool) bool {
		return false
	})
	return nil
}

// SetCapacity for cache
func (me *MemoryCache) SetCapacity(capacity int64) {
	me.mu.Lock()
	defer me.mu.Unlock()

	log.Debugf("Setting max memory size to %#v bytes", capacity)
	me.capacity = capacity
}

// Get information for MemoryCache
func (me *MemoryCache) Info() (ret filecache.CacheInfo) {
	me.mu.Lock()
	defer me.mu.Unlock()
	ret.Capacity = me.capacity
	ret.Filled = me.filled
	ret.NumItems = len(me.items)
	return
}

func (me *MemoryCache) Stat(path string) (os.FileInfo, error) {
	buf, err := me.OpenBuffer(path)
	if err != nil {
		return nil, err
	}
	return filebuffer.GetStats(path, buf.f), nil
}

func (me *MemoryCache) AsResourceProvider() resource.Provider {
	return &uniformResourceProvider{me}
}

// An empty return path is an error.
func sanitizePath(p string) (ret key) {
	if p == "" {
		return
	}
	ret = key(path.Clean("/" + p))
	if ret[0] == '/' {
		ret = ret[1:]
	}
	return
}
