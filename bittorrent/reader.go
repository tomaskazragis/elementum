package bittorrent

import (
	"math/rand"
	"time"

	gotorrent "github.com/anacrolix/torrent"
)

type Reader struct {
	*gotorrent.Reader
	*gotorrent.File
	*Torrent

	id     int32
	curPos int64
}

func (t *Torrent) NewReader(f *gotorrent.File) *Reader {
	rand.Seed(time.Now().UTC().UnixNano())

	reader := &Reader{
		Reader:  t.Torrent.NewReader(),
		File:    f,
		Torrent: t,

		id: rand.Int31(),
	}
	reader.Reader.SetReadahead(1)
	log.Debugf("NewReader: %#v", reader)

	t.readers = append(t.readers, reader)
	// log.Debugf("Active readers: %#v", t.readers)
	// for i, r := range t.readers {
	// 	log.Debugf("Active reader: %#v = %#v === %#v", i, *r, *r.Reader)
	// }

	return reader
}

func (r *Reader) Close() error {
	log.Debugf("Closing reader: %#v", r.id)

	r.Torrent.mu.Lock()
	defer r.Torrent.mu.Unlock()

	for i := 0; i < len(r.Torrent.readers); i++ {
		if r.Torrent.readers[i].id == r.id {
			r.Torrent.readers = append(r.Torrent.readers[:i], r.Torrent.readers[i+1:]...)
			break
		}
	}

	return r.Reader.Close()
}
