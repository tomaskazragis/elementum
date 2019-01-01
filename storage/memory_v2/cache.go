package memory

import (
	"math"
	"runtime/debug"
	"time"

	"github.com/anacrolix/missinggo/perf"
	"github.com/anacrolix/sync"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	humanize "github.com/dustin/go-humanize"

	"github.com/RoaringBitmap/roaring"

	"github.com/elgatito/elementum/bittorrent/reader"
	estorage "github.com/elgatito/elementum/storage"
)

// Cache ...
type Cache struct {
	s *Storage

	mu  *sync.Mutex
	bmu *sync.Mutex
	rmu *sync.Mutex

	id string

	capacity  int64
	readahead int64

	pieceCount  int
	pieceLength int64
	pieces      map[int]*Piece

	bufferSize  int
	bufferLimit int
	bufferUsed  int
	buffers     []*Buffer

	reservedPieces *roaring.Bitmap
	reserved       []int

	readerPieces *roaring.Bitmap
	readers      []*reader.PositionReader
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
	return c.readahead - (c.pieceLength * int64((c.bufferSize - c.bufferLimit)))
}

// SetReadaheadSize ...
func (c *Cache) SetReadaheadSize(size int64) {
	log.Debugf("Setting readahead size to: %s", humanize.Bytes(uint64(size)))

	c.readahead = size
}

// SetReaders ...
func (c *Cache) SetReaders(readers []*reader.PositionReader) {
	c.readers = readers
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

	return c.pieces[m.Index()]
}

// Init creates buffers and underlying maps
func (c *Cache) Init(info *metainfo.Info) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.reservedPieces = roaring.NewBitmap()
	c.readerPieces = roaring.NewBitmap()

	c.pieceCount = info.NumPieces()
	c.pieceLength = info.PieceLength
	c.pieces = map[int]*Piece{}

	// Using max possible buffers + 2
	c.bufferSize = int(math.Ceil(float64(c.capacity)/float64(c.pieceLength)) + 2)
	if c.bufferSize > c.pieceCount {
		c.bufferSize = c.pieceCount
	}
	c.bufferLimit = c.bufferSize
	c.buffers = make([]*Buffer, c.bufferSize)

	c.readahead = c.capacity

	log.Infof("Init memory for torrent. Buffers: %d, Piece length: %s, Capacity: %s", c.bufferSize, humanize.Bytes(uint64(c.pieceLength)), humanize.Bytes(uint64(c.capacity)))

	for _, idx := range c.reserved {
		c.reservedPieces.Add(uint32(idx))
	}

	for i := 0; i < c.pieceCount; i++ {
		c.pieces[i] = &Piece{
			c:      c,
			index:  i,
			length: info.Piece(i).Length(),
			mu:     &sync.RWMutex{},
		}
	}

	for i := range c.buffers {
		c.buffers[i] = &Buffer{
			c:      c,
			mu:     &sync.RWMutex{},
			pi:     -1,
			index:  i,
			buffer: make([]byte, c.pieceLength),
		}
	}
}

// Close ...
func (c *Cache) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.buffers = nil
	c.pieces = nil

	debug.FreeOSMemory()

	return nil
}

// RemovePiece ...
func (c *Cache) RemovePiece(idx int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.pieces[idx]; ok {
		c.remove(idx)
	}
}

func (c *Cache) remove(pi int) {
	// Don't allow to delete reserved pieces
	if !c.pieces[pi].buffered() || c.pieces[pi].b.reserved() {
		return
	}

	defer perf.ScopeTimer()()

	// log.Debugf("Removing element: %#v", pi)

	c.pieces[pi].b.Reset()
	c.pieces[pi].Reset()

	c.bufferUsed--
}

func (c *Cache) trim() {
	if c.capacity < 0 || c.bufferUsed < c.bufferLimit {
		return
	}

	defer perf.ScopeTimer()()

	c.bmu.Lock()
	defer c.bmu.Unlock()

	if c.readers != nil {
		c.rmu.Lock()
		c.readerPieces.Clear()
		for _, r := range c.readers {
			pr := r.PiecesRange()
			if pr.Begin < pr.End {
				c.readerPieces.AddRange(uint64(pr.Begin), uint64(pr.End))
			} else {
				c.readerPieces.AddInt(pr.Begin)
			}
		}
		c.rmu.Unlock()
	}

	for c.bufferUsed >= c.bufferLimit {
		// log.Debugf("Trimming: %#v to %#v", c.bufferUsed, c.bufferLimit)
		if !c.readerPieces.IsEmpty() {
			var minIndex int
			var minTime time.Time

			for _, b := range c.buffers {
				if b.used && b.assigned() && !b.reserved() && !b.readed() && (minIndex == 0 || b.accessed.Before(minTime)) {
					minIndex = b.pi
					minTime = b.accessed
				}
			}
			if minIndex > 0 {
				c.remove(minIndex)
				continue
			}
		}

		var minIndex int
		var minTime time.Time

		for _, b := range c.buffers {
			if b.used && b.assigned() && !b.reserved() && (minIndex == 0 || b.accessed.Before(minTime)) {
				minIndex = b.pi
				minTime = b.accessed
			}
		}

		if minIndex > 0 {
			c.remove(minIndex)
			continue
		}
	}
}
