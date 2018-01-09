package storage

import (
	"sync"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

type mapPieceCompletion struct {
	mu sync.Mutex
	m  map[metainfo.PieceKey]bool
}

var _ CustomPieceCompletion = (*mapPieceCompletion)(nil)

// NewMapPieceCompletion default Mmap Completion
func NewMapPieceCompletion() CustomPieceCompletion {
	return &mapPieceCompletion{m: make(map[metainfo.PieceKey]bool)}
}

func (*mapPieceCompletion) Close() error { return nil }

func (*mapPieceCompletion) Attach(ih metainfo.Hash, ti *metainfo.Info) error { return nil }
func (*mapPieceCompletion) Detach(ih metainfo.Hash) error                    { return nil }

func (me *mapPieceCompletion) Get(pk metainfo.PieceKey) (c storage.Completion, err error) {
	me.mu.Lock()
	defer me.mu.Unlock()
	c.Complete, c.Ok = me.m[pk]
	return
}

func (me *mapPieceCompletion) Set(pk metainfo.PieceKey, b bool) error {
	me.mu.Lock()
	defer me.mu.Unlock()
	if me.m == nil {
		me.m = make(map[metainfo.PieceKey]bool)
	}
	me.m[pk] = b
	return nil
}
