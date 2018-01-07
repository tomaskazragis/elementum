package bittorrent

import (
	"math/rand"
	"path/filepath"
	"time"

	gotorrent "github.com/anacrolix/torrent"

	"github.com/elgatito/elementum/broadcast"
	"github.com/elgatito/elementum/util"
	"github.com/elgatito/elementum/xbmc"
)

// FileReader ...
type FileReader struct {
	gotorrent.Reader
	*gotorrent.File
	*Torrent

	lb *broadcast.Broadcaster
	id int
}

// NewFileReader ...
func NewFileReader(t *Torrent, f *gotorrent.File, rmethod string) (*FileReader, error) {
	rand.Seed(time.Now().UTC().UnixNano())

	fr := &FileReader{
		Reader:  f.NewReader(),
		File:    f,
		Torrent: t,

		lb: broadcast.LocalBroadcasters[broadcast.WATCHED],
		id: int(rand.Int31()),
	}
	fr.Reader.SetReadahead(1)
	log.Debugf("NewReader: %#v", fr.id)

	if rmethod == "GET" {
		t.readers[fr.id] = fr
		log.Debugf("Active readers: %#v", len(t.readers))

		log.Infof("Setting readahead for reader %d as %d", fr.id, t.Storage().GetReadaheadSize())
		fr.SetReadahead(t.Storage().GetReadaheadSize())
	}

	fr.setSubtitles()

	return fr, nil
}

// Close ...
func (fr *FileReader) Close() error {
	log.Debugf("Closing reader: %#v", fr.id)

	if item := fr.Torrent.GetPlayingItem(); item != nil {
		go fr.lb.Broadcast(item)
	}

	fr.Torrent.mu.Lock()
	defer fr.Torrent.mu.Unlock()

	delete(fr.Torrent.readers, fr.id)
	log.Debugf("Active readers: %#v", len(fr.Torrent.readers))

	return fr.Reader.Close()
}

func (fr *FileReader) setSubtitles() {
	filePath := fr.File.Path()
	extension := filepath.Ext(filePath)

	if extension != ".srt" {
		srtPath := filePath[0:len(filePath)-len(extension)] + ".srt"
		files := fr.Torrent.Files()

		for _, f := range files {
			if f.Path() == srtPath {
				xbmc.PlayerSetSubtitles(util.GetHTTPHost() + "/files/" + srtPath)
				return
			}
		}
	}
}
