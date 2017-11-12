package bittorrent

import (
	"time"
	"math/rand"

	gotorrent "github.com/anacrolix/torrent"
)

type Reader struct {
	*gotorrent.Reader
	*gotorrent.File
	*Torrent

	id int32
	// closing chan struct{}
}

func (t *Torrent) NewReader(f *gotorrent.File) *Reader {
	rand.Seed(time.Now().UTC().UnixNano())

	reader := &Reader{
		Reader: t.Torrent.NewReader(),
		File:   f,
		Torrent: t,

		id: rand.Int31(),
		// closing: make(chan struct{}, 1),
	}

	log.Debugf("NewReader: %#v", reader)

	// go reader.Watch()
	return reader
}

// func (r *Reader) Watch() {
// 	ticker := time.NewTicker(10 * time.Second)
//
// 	defer ticker.Stop()
//
// 	for {
// 		select {
// 		case <- ticker.C:
// 			r.Torrent.CurrentPos(r.Reader.CurrentPos(), r.File)
// 		case <- r.closing:
// 			return
// 		}
// 	}
// }

func (r *Reader) Close() error {
	log.Debugf("Closing reader: %#v", r.id)
	// r.closing <- struct{}{}
	// defer close(r.closing)

	return r.Reader.Close()
}

func (r *Reader) Seek(off int64, whence int) (int64, error) {
	// r.Torrent.CurrentPos(r.Reader.CurrentPos(), r.File)
	return r.Reader.Seek(off, whence)
}
