package storage

import (
	"github.com/anacrolix/torrent/storage"

	fat32storage "github.com/iamacarpet/go-torrent-storage-fat32"
)

const (
	StorageFile = iota
	StorageMemory
	StorageFat32
	StorageMMap
)

type ElementumStorage interface {
	storage.ClientImpl

	Start()
	Stop()
	SyncPieces(map[int]bool)
	RemovePiece(int)
	GetReadaheadSize() int64
	SetReadaheadSize(int64)
}

type DummyStorage struct {
	storage.ClientImpl
	Type      int
	readahead int64
}

func NewFat32Storage(path string) ElementumStorage {
	return &DummyStorage{fat32storage.NewFat32Storage(path), StorageFat32, 0}
}

func NewFileStorage(path string, pc storage.PieceCompletion) ElementumStorage {
	return &DummyStorage{storage.NewFileWithCompletion(path, pc), StorageFile, 0}
}

func NewMMapStorage(path string, pc storage.PieceCompletion) ElementumStorage {
	return &DummyStorage{storage.NewMMapWithCompletion(path, pc), StorageMMap, 0}
}

func (me *DummyStorage) Start()                    {}
func (me *DummyStorage) Stop()                     {}
func (me *DummyStorage) SyncPieces(a map[int]bool) {}
func (me *DummyStorage) RemovePiece(idx int)       {}

func (me *DummyStorage) GetReadaheadSize() int64 {
	return me.readahead
}

func (me *DummyStorage) SetReadaheadSize(size int64) {
	me.readahead = size
}
