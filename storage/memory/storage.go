package memory

import (
	"sync"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"

	"github.com/op/go-logging"

	estorage "github.com/elgatito/elementum/storage"
)

const (
	chunkSize      = 1024 * 16
	readaheadRatio = 0.33
)

var log = logging.MustGetLogger("memory")

// Storage main object
type Storage struct {
	Type     int
	mu       *sync.Mutex
	items    map[string]*Cache
	capacity int64
}

// NewMemoryStorage initializer function
func NewMemoryStorage(maxMemorySize int64) *Storage {
	log.Debugf("Initializing memory storage of size: %#v", maxMemorySize)
	s := &Storage{
		mu:       &sync.Mutex{},
		capacity: maxMemorySize,
		items:    map[string]*Cache{},
	}

	return s
}

// GetTorrentStorage ...
func (s *Storage) GetTorrentStorage(hash string) estorage.TorrentStorage {
	if i, ok := s.items[hash]; ok {
		return i
	}

	return nil
}

// Close ...
func (s *Storage) Close() error {
	return nil
}

// GetReadaheadSize ...
func (s *Storage) GetReadaheadSize() int64 {
	return 0
}

// SetReadaheadSize ...
func (s *Storage) SetReadaheadSize(size int64) {}

// OpenTorrent ...
func (s *Storage) OpenTorrent(info *metainfo.Info, infoHash metainfo.Hash) (storage.TorrentImpl, error) {
	c := &Cache{
		s:        s,
		capacity: s.capacity,
		id:       infoHash.HexString(),
		mu:       &sync.Mutex{},
	}
	c.Init(info)
	go c.Start()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[c.id] = c

	return c, nil
}
