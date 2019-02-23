package bittorrent

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	lt "github.com/ElementumOrg/libtorrent-go"
	"github.com/anacrolix/missinggo"
	"github.com/anacrolix/missinggo/perf"

	"github.com/elgatito/elementum/database"
	"github.com/elgatito/elementum/util"
	"github.com/elgatito/elementum/xbmc"
)

const (
	piecesRefreshDuration = 500 * time.Millisecond
)

// TorrentFS ...
type TorrentFS struct {
	http.Dir
	s *Service
}

// TorrentFSEntry ...
type TorrentFSEntry struct {
	http.File
	tfs *TorrentFS
	t   *Torrent
	f   *File

	totalLength int64
	pieceLength int
	numPieces   int

	removed missinggo.Event
	dbItem  *database.BTItem

	id          int64
	readahead   int64
	storageType int

	lastUsed time.Time
}

// PieceRange ...
type PieceRange struct {
	Begin, End int
}

// NewTorrentFS ...
func NewTorrentFS(service *Service) *TorrentFS {
	return &TorrentFS{
		s:   service,
		Dir: http.Dir(service.config.DownloadPath),
	}
}

// Open ...
func (tfs *TorrentFS) Open(uname string) (http.File, error) {
	name := util.DecodeFileURL(uname)

	var file http.File
	var err error

	if tfs.s.config.DownloadStorage == StorageFile {
		file, err = os.Open(filepath.Join(string(tfs.Dir), name))
		if err != nil {
			return nil, err
		}

		// make sure we don't open a file that's locked, as it can happen
		// on BSD systems (darwin included)
		if err := unlockFile(file.(*os.File)); err != nil {
			log.Errorf("Unable to unlock file because: %s", err)
		}
	}

	log.Infof("Opening %s", name)

	for _, t := range tfs.s.q.All() {
		for _, f := range t.files {
			log.Debugf("File: %#v, name: %#v", f, name[1:])
			if name[1:] == f.Path {
				log.Noticef("%s belongs to torrent %s", name, t.Name())
				return NewTorrentFSEntry(file, tfs, t, f, name)
			}
		}
	}

	return file, err
}

// NewTorrentFSEntry ...
func NewTorrentFSEntry(file http.File, tfs *TorrentFS, t *Torrent, f *File, name string) (*TorrentFSEntry, error) {
	if file == nil {
		file = NewMemoryFile(tfs, t.th.GetMemoryStorage(), f, name)
	}

	tf := &TorrentFSEntry{
		File: file,
		tfs:  tfs,
		t:    t,
		f:    f,

		totalLength: t.ti.TotalSize(),
		pieceLength: t.ti.PieceLength(),
		numPieces:   t.ti.NumPieces(),
		storageType: tfs.s.config.DownloadStorage,
		id:          time.Now().UTC().UnixNano(),

		lastUsed: time.Now(),
	}
	go tf.consumeAlerts()

	t.muReaders.Lock()
	t.readers[tf.id] = tf
	log.Debugf("Added reader. Active readers now: %#v", len(t.readers))
	t.muReaders.Unlock()

	t.ResetReaders()

	tf.setSubtitles()

	return tf, nil
}

func (tf *TorrentFSEntry) consumeAlerts() {
	alerts, done := tf.tfs.s.Alerts()
	defer close(done)

	for alert := range alerts {
		switch alert.Type {
		case lt.TorrentRemovedAlertAlertType:
			removedAlert := lt.SwigcptrTorrentAlert(alert.Pointer)
			if removedAlert.GetHandle().Equal(tf.t.th) {
				tf.removed.Set()
				return
			}
		}
	}
}

func (tf *TorrentFSEntry) setSubtitles() {
	filePath := tf.f.Path
	extension := filepath.Ext(filePath)

	if extension != ".srt" {
		srtPath := filePath[0:len(filePath)-len(extension)] + ".srt"

		for _, f := range tf.t.files {
			if f.Path == srtPath {
				xbmc.PlayerSetSubtitles(util.GetHTTPHost() + "/files/" + srtPath)
				return
			}
		}
	}
}

// Close ...
func (tf *TorrentFSEntry) Close() error {
	defer perf.ScopeTimer()()

	log.Info("Closing file...")
	tf.removed.Set()

	if tf.tfs.s == nil || tf.t.Closer.IsSet() || tf.tfs.s.Closer.IsSet() {
		return nil
	}

	tf.t.muReaders.Lock()
	delete(tf.t.readers, tf.id)
	log.Debugf("Removed reader. Active readers left: %#v", len(tf.t.readers))
	tf.t.muReaders.Unlock()

	tf.t.ResetReaders()

	return tf.File.Close()
}

// Read ...
func (tf *TorrentFSEntry) Read(data []byte) (n int, err error) {
	defer perf.ScopeTimer()()
	tf.lastUsed = time.Now()

	currentOffset, err := tf.File.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}

	left := len(data)
	pos := 0
	piece, pieceOffset := tf.pieceFromOffset(currentOffset)

	for left > 0 && err == nil {
		size := left

		if err = tf.waitForPiece(piece); err != nil {
			log.Debugf("Wait failed: %d", piece)
			continue
		}

		if pieceOffset+size > tf.pieceLength {
			size = tf.pieceLength - pieceOffset
		}

		b := data[pos : pos+size]
		n1 := 0

		if tf.storageType == StorageMemory {
			n1, err = tf.File.(*MemoryFile).ReadPiece(b, piece, pieceOffset)
		} else {
			n1, err = tf.File.Read(b)
		}

		if err != nil {
			if err == io.ErrShortBuffer {
				log.Debugf("Retrying to fetch piece: %d", piece)
				err = nil
				time.Sleep(500 * time.Millisecond)
				continue
			}
			return
		} else if n1 > 0 {
			n += n1
			left -= n1
			pos += n1

			currentOffset += int64(n1)
			piece, pieceOffset = tf.pieceFromOffset(currentOffset)
			// if n1 < len(data) {
			// 	log.Debugf("Move slice: n1=%d, req=%d, current=%d, piece=%d, offset=%d", n1, len(b), currentOffset, piece, pieceOffset)
			// }
		} else {
			return
		}
	}

	return
}

// Seek ...
func (tf *TorrentFSEntry) Seek(offset int64, whence int) (int64, error) {
	defer perf.ScopeTimer()()
	tf.lastUsed = time.Now()

	seekingOffset := offset

	switch whence {
	case io.SeekCurrent:
		currentOffset, err := tf.File.Seek(0, io.SeekCurrent)
		if err != nil {
			return currentOffset, err
		}
		seekingOffset += currentOffset
		break
	case io.SeekEnd:
		seekingOffset = tf.f.Size - offset
		break
	}

	log.Infof("Seeking at %d... with %d", seekingOffset, whence)
	return tf.File.Seek(offset, whence)

	// piece, _ := tf.pieceFromOffset(seekingOffset)
	// if tf.t.hasPiece(piece) == false {
	// 	log.Infof("We don't have piece %d, setting piece priorities", piece)

	// 	// TODO: Need this? Cleaning all the deadlines and call PrioritizePieces to reorganize priorities
	// 	// tf.t.th.ClearPieceDeadlines()
	// 	tf.t.PrioritizePiece(piece)
	// 	go tf.t.PrioritizePieces()
	// }

	// return tf.File.Seek(offset, whence)
}

func (tf *TorrentFSEntry) waitForPiece(piece int) error {
	if tf.t.hasPiece(piece) {
		return nil
	}

	defer perf.ScopeTimer()()
	log.Infof("Waiting for piece %d", piece)
	now := time.Now()
	defer func() {
		log.Infof("Waiting for piece %d finished in %s", piece, time.Since(now))
	}()

	tf.t.PrioritizePiece(piece)

	pieceRefreshTicker := time.Tick(piecesRefreshDuration)
	removed := tf.removed.C()

	for tf.t.hasPiece(piece) == false {
		select {
		case <-removed:
			log.Warningf("Unable to wait for piece %d as file was closed", piece)
			return errors.New("File was closed")
		case <-pieceRefreshTicker:
			continue
		}
	}

	tf.t.awaitingPieces.Remove(uint32(piece))
	return nil
}

func (tf *TorrentFSEntry) pieceFromOffset(offset int64) (int, int) {
	if tf.pieceLength == 0 {
		return 0, 0
	}

	piece := (tf.f.Offset + offset) / int64(tf.pieceLength)
	pieceOffset := (tf.f.Offset + offset) % int64(tf.pieceLength)
	return int(piece), int(pieceOffset)
}

// ReaderPiecesRange ...
func (tf *TorrentFSEntry) ReaderPiecesRange() (ret PieceRange) {
	ra := tf.readahead
	if ra < 1 {
		// Needs to be at least 1, because [x, x) means we don't want
		// anything.
		ra = 1
	}
	pos, _ := tf.File.Seek(0, io.SeekCurrent)
	if tf.f.Size > 0 && ra > tf.f.Size-pos {
		ra = tf.f.Size - pos
	}
	log.Infof("Range: id=%d, pos=%d, ra=%d, orig=%d, size=%d", tf.id, pos, ra, tf.readahead, tf.f.Size)

	return tf.byteRegionPieces(tf.torrentOffset(pos), ra)
}

func (tf *TorrentFSEntry) torrentOffset(readerPos int64) int64 {
	return tf.f.Offset + readerPos
}

// Returns the range of pieces [begin, end) that contains the extent of bytes.
func (tf *TorrentFSEntry) byteRegionPieces(off, size int64) (pr PieceRange) {
	if off >= tf.totalLength {
		return
	}
	if off < 0 {
		size += off
		off = 0
	}
	if size <= 0 {
		return
	}

	pl := int64(tf.pieceLength)
	pr.Begin = util.Max(0, int(off/pl))
	pr.End = util.Min(tf.numPieces-1, int((off+size-1)/pl))

	return
}

// IsIdle ...
func (tf *TorrentFSEntry) IsIdle() bool {
	return tf.lastUsed.Before(time.Now().Add(time.Minute * -1))
}
