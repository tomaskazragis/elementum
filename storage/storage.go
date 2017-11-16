package storage

import (
	"github.com/anacrolix/torrent/storage"
)

type ElementumStorage interface {
	storage.ClientImpl

	Start()
	Stop()
	Seek()
}
