package storage

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/boltdb/bolt"
	"github.com/op/go-logging"
	"github.com/vmihailenco/msgpack"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

//go:generate msgp -o msgp.go -io=false -tests=false
//msgp:ignore boltBackedPieceCompletion

type boltBackedPieceCompletion struct {
	db *bolt.DB
	mu sync.Mutex
	al map[metainfo.Hash]*attachItem
	m  map[metainfo.PieceKey]bool
}

type attachItem struct {
	infoHash string
	pieces   []metainfo.PieceKey
	done     chan struct{}
}

// SerializedPiece used to store/restore pieces completion from the database
type SerializedPiece struct {
	I int
	H string
	C bool
}

var _ CustomPieceCompletion = (*boltBackedPieceCompletion)(nil)

var (
	log                 = logging.MustGetLogger("pc")
	completionBucketKey = []byte("sync")
)

var _ CustomPieceCompletion = (*boltBackedPieceCompletion)(nil)

// NewBoltBackedPieceCompletion provides a Completion factory, which
// syncronizes completions into the bolt database
func NewBoltBackedPieceCompletion(dir string) (ret CustomPieceCompletion, err error) {
	os.MkdirAll(dir, 0770)
	p := filepath.Join(dir, ".torrent.bolt.db")
	db, err := bolt.Open(p, 0660, &bolt.Options{
		Timeout: time.Second,
	})
	if err != nil {
		return
	}
	ret = &boltBackedPieceCompletion{
		db: db,
		m:  map[metainfo.PieceKey]bool{},
		al: map[metainfo.Hash]*attachItem{},
	}
	return
}

// Get ...
func (me *boltBackedPieceCompletion) Get(pk metainfo.PieceKey) (c storage.Completion, err error) {
	me.mu.Lock()
	defer me.mu.Unlock()
	c.Complete, c.Ok = me.m[pk]
	return
}

// Set ...
func (me *boltBackedPieceCompletion) Set(pk metainfo.PieceKey, b bool) error {
	me.mu.Lock()
	defer me.mu.Unlock()
	me.m[pk] = b
	return nil
}

// Attach ...
func (me *boltBackedPieceCompletion) Attach(ih metainfo.Hash, ti *metainfo.Info) error {
	if ti == nil {
		return errors.New("Torrent metainfo is empty")
	}

	log.Debugf("Attaching item: %#v", ih.HexString())
	a := &attachItem{
		done:   make(chan struct{}, 1),
		pieces: make([]metainfo.PieceKey, ti.NumPieces()),
	}

	for i := 0; i < ti.NumPieces(); i++ {
		a.pieces[i] = metainfo.PieceKey{
			InfoHash: ti.Piece(i).Hash(),
			Index:    ti.Piece(i).Index(),
		}
	}
	me.al[ih] = a

	me.GetBackup(ih)
	go func() {
		tickerBackup := time.NewTicker(5 * time.Second)
		defer tickerBackup.Stop()
		defer close(a.done)

		for {
			select {
			case <-tickerBackup.C:
				me.SetBackup(ih)
			case <-a.done:
				me.SetBackup(ih)
				return
			}
		}
	}()

	return nil
}

// Detach ...
func (me *boltBackedPieceCompletion) Detach(ih metainfo.Hash) error {
	a, ok := me.al[ih]
	if !ok {
		return errors.New("Torrent not found in completion")
	}

	log.Debugf("Detaching item: %#v", ih.HexString())

	a.done <- struct{}{}
	delete(me.al, ih)

	return nil
}

// Close ...
func (me *boltBackedPieceCompletion) Close() error {
	// serializing attached torrents
	for i := range me.al {
		me.Detach(i)
	}

	return me.db.Close()
}

// GetBackup ...
func (me *boltBackedPieceCompletion) GetBackup(ih metainfo.Hash) (err error) {
	if _, ok := me.al[ih]; !ok {
		return errors.New("Torrent not attached")
	}

	return me.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(completionBucketKey)
		if b == nil {
			log.Debugf("Bucket not exists: %#v", ih)
			return nil
		}
		pieces := []SerializedPiece{}
		buf := bytes.NewBuffer(b.Get([]byte(ih.HexString())))
		if errDecode := msgpack.NewDecoder(buf).Decode(&pieces); errDecode != nil {
			return errDecode
		}

		me.mu.Lock()
		for _, p := range pieces {
			if len(p.H) < 20 {
				continue
			}
			me.m[metainfo.PieceKey{InfoHash: metainfo.NewHashFromHex(p.H), Index: p.I}] = p.C
		}
		me.mu.Unlock()

		return nil
	})
}

// SetBackup ...
func (me *boltBackedPieceCompletion) SetBackup(ih metainfo.Hash) error {
	if _, ok := me.al[ih]; !ok {
		return errors.New("Torrent not attached")
	}

	return me.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(completionBucketKey)
		if err != nil {
			return err
		}

		buf := bytes.NewBuffer(nil)

		me.mu.Lock()
		pieces := make([]SerializedPiece, len(me.al[ih].pieces))
		for _, p := range me.al[ih].pieces {
			pieces[p.Index].I = p.Index
			pieces[p.Index].H = p.InfoHash.HexString()
			pieces[p.Index].C = me.m[metainfo.PieceKey{InfoHash: p.InfoHash, Index: p.Index}]
		}
		me.mu.Unlock()

		if errDecode := msgpack.NewEncoder(buf).Encode(pieces); errDecode != nil {
			return errDecode
		}

		return b.Put([]byte(ih.HexString()), buf.Bytes())
	})
}
