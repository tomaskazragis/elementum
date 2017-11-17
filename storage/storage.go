package storage

import (
	"github.com/anacrolix/torrent/storage"

	fat32storage "github.com/iamacarpet/go-torrent-storage-fat32"
)

type ElementumStorage interface {
	storage.ClientImpl

	Start()
	Stop()
	Seek()
}

type DummyStorage struct {
	storage.ClientImpl
}

func NewFat32Storage(path string) ElementumStorage {
	return &DummyStorage{fat32storage.NewFat32Storage(path)}
}

func NewFileStorage(path string, pc storage.PieceCompletion) ElementumStorage {
	return &DummyStorage{storage.NewFileWithCompletion(path, pc)}
}

func (me *DummyStorage) Seek()  {}
func (me *DummyStorage) Start() {}
func (me *DummyStorage) Stop()  {}
