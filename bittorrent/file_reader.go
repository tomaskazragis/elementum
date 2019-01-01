package bittorrent

import (
	"context"
	"path/filepath"
	"time"

	gotorrent "github.com/anacrolix/torrent"

	"github.com/elgatito/elementum/bittorrent/reader"
	"github.com/elgatito/elementum/util"
	"github.com/elgatito/elementum/xbmc"
)

// FileReader ...
type FileReader struct {
	gotorrent.Reader
	*gotorrent.File
	*Torrent
	*reader.PositionReader

	id int64
}

// NewFileReader ...
func NewFileReader(t *Torrent, f *gotorrent.File, rmethod string) (*FileReader, error) {
	fr := &FileReader{
		Reader:  f.NewReader(),
		File:    f,
		Torrent: t,
		PositionReader: &reader.PositionReader{
			PieceLength: int64(t.pieceLength),
			FileLength:  f.Length(),
			Offset:      f.Offset(),
			Pieces:      t.Torrent.Info().NumPieces(),
		},

		id: time.Now().UTC().UnixNano(),
	}
	fr.SetReadahead(1)

	log.Debugf("NewReader: %#v", fr.id)

	if rmethod == "GET" {
		// t.CloseReaders()

		t.muReaders.Lock()
		t.readers[fr.id] = fr
		log.Debugf("Active readers now: %#v", len(t.readers))
		t.muReaders.Unlock()

		// log.Infof("Setting readahead for reader %d as %s", fr.id, humanize.Bytes(uint64(t.GetReadaheadSize())))
		// fr.SetReadahead(t.GetReadaheadSize())

		t.ResetReaders()
		t.SetReaders()
	}

	fr.setSubtitles()

	return fr, nil
}

// Close ...
func (fr *FileReader) Close() (err error) {
	log.Debugf("Closing reader: %#v", fr.id)

	err = fr.Reader.Close()
	if fr.Service == nil || fr.Service.ShuttingDown {
		return
	}

	fr.Torrent.muReaders.Lock()
	delete(fr.Torrent.readers, fr.id)
	log.Debugf("Active readers left: %#v", len(fr.Torrent.readers))
	fr.Torrent.muReaders.Unlock()

	fr.Torrent.ResetReaders()
	return
}

// Read ...
func (fr *FileReader) Read(b []byte) (n int, err error) {
	n, err = fr.Reader.Read(b)
	if err == nil {
		fr.PositionReader.Pos += int64(n)
	}

	return
}

// ReadContext ...
func (fr *FileReader) ReadContext(ctx context.Context, b []byte) (n int, err error) {
	n, err = fr.Reader.ReadContext(ctx, b)
	if err == nil {
		fr.PositionReader.Pos += int64(n)
	}

	return
}

// Seek ...
func (fr *FileReader) Seek(off int64, whence int) (ret int64, err error) {
	ret, err = fr.Reader.Seek(off, whence)
	if err == nil {
		fr.PositionReader.Pos = ret
	}

	return
}

// SetReadahead ...
func (fr *FileReader) SetReadahead(ra int64) {
	fr.Reader.SetReadahead(ra)
	fr.PositionReader.Readahead = ra
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
