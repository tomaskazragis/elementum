package storage

import (
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"github.com/elgatito/elementum/bittorrent/reader"

	fat32storage "github.com/iamacarpet/go-torrent-storage-fat32"
)

const (
	// StorageFile Default file storage
	StorageFile = iota
	// StorageMemory In-memory storage
	StorageMemory
	// StorageFat32 file splitted into small files
	StorageFat32
	// StorageMMap MMap file storage
	StorageMMap
)

// Storages lists basic names of used storage engines
var Storages = map[int]string{
	StorageFile:   "File",
	StorageMMap:   "MMap",
	StorageFat32:  "Fat32",
	StorageMemory: "Memory",
}

// ElementumStorage basic interface for storages, used in the plugin
type ElementumStorage interface {
	storage.ClientImpl

	// Start()
	// Stop()
	// SyncPieces(map[int]bool)
	// RemovePiece(int)
	GetTorrentStorage(string) TorrentStorage
	GetReadaheadSize() int64
	SetReadaheadSize(int64)
	SetReaders([]*reader.PositionReader)
}

// TorrentStorage ...
type TorrentStorage interface {
	ElementumStorage
}

// DummyStorage ...
type DummyStorage struct {
	storage.ClientImpl
	Type      int
	readahead int64
}

// CustomPieceCompletion own override of PieceCompletion
type CustomPieceCompletion interface {
	storage.PieceCompletion
	Attach(metainfo.Hash, *metainfo.Info) error
	Detach(metainfo.Hash) error
}

// NewFat32Storage ...
func NewFat32Storage(path string) ElementumStorage {
	return &DummyStorage{fat32storage.NewFat32Storage(path), StorageFat32, 0}
}

// NewFileStorage ...
func NewFileStorage(path string, pc storage.PieceCompletion) ElementumStorage {
	return &DummyStorage{storage.NewFileWithCompletion(path, pc), StorageFile, 0}
}

// NewMMapStorage ...
func NewMMapStorage(path string, pc storage.PieceCompletion) ElementumStorage {
	return &DummyStorage{storage.NewMMapWithCompletion(path, pc), StorageMMap, 0}
}

// Start ...
func (me *DummyStorage) Start() {}

// Stop ...
func (me *DummyStorage) Stop() {}

// func (me *DummyStorage) SyncPieces(a map[int]bool)                    {}
// func (me *DummyStorage) RemovePiece(idx int)                          {}

// GetTorrentStorage ...
func (me *DummyStorage) GetTorrentStorage(hash string) TorrentStorage { return me }

// GetReadaheadSize ...
func (me *DummyStorage) GetReadaheadSize() int64 {
	return me.readahead
}

// SetReadaheadSize ...
func (me *DummyStorage) SetReadaheadSize(size int64) {
	me.readahead = size
}

// SetReaders ...
func (me *DummyStorage) SetReaders(readers []*reader.PositionReader) {
}
