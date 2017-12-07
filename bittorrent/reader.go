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

	id int
}

func (t *Torrent) NewReader(f *gotorrent.File, isget bool) *Reader {
	rand.Seed(time.Now().UTC().UnixNano())

	reader := &Reader{
		Reader:  t.Torrent.NewReader(),
		File:    f,
		Torrent: t,

		id: int(rand.Int31()),
	}
	reader.Reader.SetReadahead(1)
	log.Debugf("NewReader: %#v", reader.id)

	if isget {
		t.readers[reader.id] = reader
		log.Debugf("Active readers: %#v", len(t.readers))
		// for i, r := range t.readers {
		// 	log.Debugf("Active reader: %#v = %#v === %#v", i, *r, *r.Reader)
		// }
	}

	return reader
}

func (r *Reader) Close() error {
	log.Debugf("Closing reader: %#v", r.id)

	r.Torrent.mu.Lock()
	defer r.Torrent.mu.Unlock()

	delete(r.Torrent.readers, r.id)

	return r.Reader.Close()
}
