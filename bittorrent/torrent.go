package bittorrent

import (
	// "fmt"
	"bytes"
	"io"
	// "math"
	"os"
	"sync"
	"time"
	// "runtime/debug"
	// "bytes"
	// "regexp"
	// "strings"
	// "net/url"
	// "net/http"
	// "crypto/sha1"
	// "encoding/hex"
	// "encoding/json"
	// "encoding/base32"
	"path/filepath"

	// "github.com/anacrolix/missinggo/pubsub"
	gotorrent "github.com/anacrolix/torrent"
	// "github.com/dustin/go-humanize"
	// "github.com/elgatito/elementum/cloudhole"
	// "github.com/elgatito/elementum/config"
	// "github.com/zeebo/bencode"

	estorage "github.com/elgatito/elementum/storage"
	"github.com/elgatito/elementum/xbmc"
)

const (
	movieType = "movie"
	showType  = "show"
)

const (
	// StatusQueued ...
	StatusQueued = iota
	// StatusChecking ...
	StatusChecking
	// StatusFinding ...
	StatusFinding
	// StatusPaused ...
	StatusPaused
	// StatusBuffering ...
	StatusBuffering
	// StatusDownloading ...
	StatusDownloading
	// StatusFinished ...
	StatusFinished
	// StatusSeeding ...
	StatusSeeding
	// StatusAllocating ...
	StatusAllocating
	// StatusStalled ...
	StatusStalled
)

// StatusStrings ...
var StatusStrings = []string{
	"Queued",
	"Checking",
	"Finding",
	"Paused",
	"Buffering",
	"Downloading",
	"Finished",
	"Seeding",
	"Allocating",
	"Stalled",
}

// Torrent ...
type Torrent struct {
	*gotorrent.Torrent

	infoHash      string
	readers       map[int]*FileReader
	bufferReaders map[string]*FileReader

	ChosenFiles []*gotorrent.File
	TorrentPath string

	Service      *BTService
	DownloadRate int64
	UploadRate   int64

	BufferLength         int64
	BufferProgress       float64
	BufferPiecesProgress map[int]float64
	BufferEndPieces      []int

	IsPlaying   bool
	IsPaused    bool
	IsBuffering bool
	IsSeeding   bool

	IsRarArchive bool

	needSeeding bool
	needDBID    bool

	DBID   int
	DBTYPE string
	DBItem *DBItem

	mu        *sync.Mutex
	muBuffer  *sync.RWMutex
	muSeeding *sync.RWMutex

	lastDownRate   int64
	downRates      []int64
	upRates        []int64
	rateCounter    int
	downloadedSize int64
	uploadedSize   int64
	dbidTries      int

	pieceLength float64

	closing        chan struct{}
	bufferFinished chan struct{}

	progressTicker *time.Ticker
	bufferTicker   *time.Ticker
	seedTicker     *time.Ticker
	dbidTicker     *time.Ticker
}

// NewTorrent ...
func NewTorrent(service *BTService, handle *gotorrent.Torrent, path string) *Torrent {
	t := &Torrent{
		infoHash: handle.InfoHash().HexString(),

		Service:     service,
		Torrent:     handle,
		TorrentPath: path,

		bufferReaders:        map[string]*FileReader{},
		readers:              map[int]*FileReader{},
		BufferPiecesProgress: map[int]float64{},
		BufferProgress:       -1,
		BufferEndPieces:      []int{},

		needSeeding: true,
		needDBID:    true,

		mu:        &sync.Mutex{},
		muBuffer:  &sync.RWMutex{},
		muSeeding: &sync.RWMutex{},

		closing: make(chan struct{}),

		seedTicker: &time.Ticker{},
		dbidTicker: &time.Ticker{},
	}

	log.Debugf("Waiting for information fetched for torrent: %#v", handle.InfoHash().HexString())
	<-t.GotInfo()
	log.Debugf("Information fetched for torrent: %#v", handle.InfoHash().HexString())
	t.Service.PieceCompletion.Attach(handle.InfoHash(), handle.Info())

	return t
}

// Storage ...
func (t *Torrent) Storage() estorage.ElementumStorage {
	return t.Service.DefaultStorage.GetTorrentStorage(t.infoHash)
}

// Watch ...
func (t *Torrent) Watch() {
	// log.Debugf("Starting watch timers")
	// debug.PrintStack()

	detailsTicker := time.NewTicker(5 * time.Second)
	t.progressTicker = time.NewTicker(1 * time.Second)
	t.bufferTicker = time.NewTicker(1 * time.Second)
	t.bufferFinished = make(chan struct{}, 5)

	t.downRates = []int64{0, 0, 0, 0, 0}
	t.upRates = []int64{0, 0, 0, 0, 0}
	t.rateCounter = 0
	t.downloadedSize = 0
	t.uploadedSize = 0

	t.dbidTries = 0

	t.pieceLength = float64(t.Torrent.Info().PieceLength)

	defer detailsTicker.Stop()
	defer t.progressTicker.Stop()
	defer t.bufferTicker.Stop()
	defer t.seedTicker.Stop()
	defer t.dbidTicker.Stop()
	defer close(t.bufferFinished)

	for {
		select {
		case <-t.bufferTicker.C:
			go t.bufferTickerEvent()

		case <-t.bufferFinished:
			go t.bufferFinishedEvent()

		case <-detailsTicker.C:
			go t.detailsEvent()

		case <-t.progressTicker.C:
			go t.progressEvent()

		case <-t.seedTicker.C:
			go t.seedTickerEvent()

		case <-t.dbidTicker.C:
			go t.dbidEvent()

		case <-t.closing:
			return
		}
	}
}

func (t *Torrent) bufferTickerEvent() {
	t.muBuffer.Lock()

	// TODO: delete when done debugging
	// completed := 0
	// checking := 0
	// partial := 0
	// total := 0

	// log.Noticef(strings.Repeat("=", 20))
	// for i := range t.BufferPiecesProgress {
	// 	total++
	// 	ps := t.PieceState(i)
	// 	if ps.Complete {
	// 		completed++
	// 	}
	// 	if ps.Checking {
	// 		checking++
	// 	}
	// 	if ps.Partial {
	// 		partial++
	// 		t.BufferPiecesProgress[i] = float64(t.PieceBytesMissing(i))
	// 	}

	// if t.PieceState(i).Complete {
	// 	continue
	// }
	//
	// log.Debugf("Piece: %d, %#v", i, t.PieceState(i))
	// }
	//log.Noticef("Total: %#v, Completed: %#v, Partial: %#v, Checking: %#v", total, completed, partial, checking)
	// log.Noticef(strings.Repeat("=", 20))

	if t.IsBuffering {
		progressCount := 0.0
		for i := range t.BufferPiecesProgress {
			progressCount += float64(t.PieceBytesMissing(i))
		}

		// progressCount := 0.0
		// for _, v := range t.BufferPiecesProgress {
		// 	progressCount += v
		// }

		total := float64(len(t.BufferPiecesProgress)) * t.pieceLength
		t.BufferProgress = (total - progressCount) / total * 100

		if t.BufferProgress >= 100 {
			t.bufferFinished <- struct{}{}
		}
	}

	t.muBuffer.Unlock()
}

// CleanupBuffer ...
func (t *Torrent) CleanupBuffer() {
	// for _, v := range t.BufferEndPieces {
	// 	t.Storage.RemovePiece(v)
	// }

	t.BufferEndPieces = []int{}
}

func (t *Torrent) detailsEvent() {
	// str := ""
	// for i := 0; i < t.Torrent.NumPieces(); i++ {
	// 	st := t.Torrent.PieceState(i)
	// 	if st.Priority > 0 {
	// 		str += fmt.Sprintf("%d:%d  ", i, st.Priority)
	// 	}
	// }
	// log.Debugf("Priorities: %s  \n", str)
	// t.Service.ClientInfo(logWriter{log})
}

func (t *Torrent) progressEvent() {
	// log.Noticef(strings.Repeat("=", 20))
	// for i := 0; i < 10; i++ {
	// 	log.Debugf("Progress Piece: %d, %#v", i, t.PieceState(i))
	// }
	// log.Noticef(strings.Repeat("=", 10))
	// for i := t.Torrent.NumPieces() - 5; i < t.Torrent.NumPieces(); i++ {
	// 	log.Debugf("Progress Piece: %d, %#v", i, t.PieceState(i))
	// }
	// log.Noticef(strings.Repeat("=", 20))

	t.downRates[t.rateCounter] = t.Torrent.BytesCompleted() - t.downloadedSize
	t.upRates[t.rateCounter] = t.Torrent.Stats().DataBytesWritten - t.uploadedSize

	t.downloadedSize = t.Torrent.BytesCompleted()
	t.uploadedSize = t.Torrent.Stats().DataBytesWritten

	t.DownloadRate = int64(average(t.downRates))
	t.UploadRate = int64(average(t.upRates))

	t.rateCounter++
	if t.rateCounter == len(t.downRates)-1 {
		t.rateCounter = 0
	}

	if t.lastDownRate == t.downloadedSize {
		buf := bytes.NewBuffer([]byte{})
		t.Service.ClientInfo(buf)
		log.Debug("Download stale. Client state:\n", buf.String())
	}
	t.lastDownRate = t.downloadedSize

	// str := ""
	// for i := 0; i < t.Torrent.NumPieces(); i++ {
	// 	// for i := 0; i < 50; i++ {
	// 	st := t.Torrent.PieceState(i)
	// 	if st.Priority > 0 {
	// 		str += fmt.Sprintf("%d:%d  ", i, st.Priority)
	// 	}
	// }
	// str += "\n"
	// for i := 0; i < 40; i++ {
	// 	st := t.Torrent.PieceState(i)
	// 	// if st.Priority > 0 {
	// 	str += fmt.Sprintf("%d:%d  ", i, st.Priority)
	// 	// }
	// }
	// log.Debugf("Priorities: %s  \n", str)
	// log.Debugf("Active readers: %#v", t.readers)
	// for i, r := range t.readers {
	// 	log.Debugf("Active reader: %#v = %#v === %#v", i, *r, *r.Reader)
	// }

	// t.Service.Client.WriteStatus(logWriter{log})

	// log.Debugf("ProgressTicker: %s; %#v/%#v; %#v = %.2f ", t.Name(), t.DownloadRate, t.UploadRate, t.GetStateString(), t.GetProgress())
	log.Debugf("PR: %#v/%#v; %#v = %.2f ", t.DownloadRate, t.UploadRate, t.GetStateString(), t.GetProgress())
	if t.needSeeding && t.Service.GetSeedTime() > 0 && t.GetProgress() >= 100 {
		t.muSeeding.Lock()
		seedingTime := time.Duration(t.Service.GetSeedTime()) * time.Hour
		log.Debugf("Starting seeding timer (%s) for: %s", seedingTime, t.Info().Name)

		t.IsSeeding = true
		t.needSeeding = false
		t.seedTicker = time.NewTicker(seedingTime)

		t.muSeeding.Unlock()
	}

	if t.DBItem == nil {
		t.GetDBItem()
	}

	// for i := 0; i < t.Torrent.NumPieces(); i++ {
	// 	state := t.Torrent.PieceState(i)
	// 	if state.Priority == 1 {
	// 		continue
	// 	} else if state.Priority == 0 {
	// 		continue
	// 	}
	// 	// } else if state.Priority == 0 && state.Complete == false {
	// 	// 	continue
	// 	// }
	//
	// 	log.Debugf("Piece with priority: %#v, %#v", i, state)
	// }
}

func (t *Torrent) bufferFinishedEvent() {
	t.muBuffer.Lock()
	log.Debugf("Buffer finished: %#v, %#v", t.IsBuffering, t.BufferPiecesProgress)

	t.IsBuffering = false

	t.muBuffer.Unlock()

	t.bufferTicker.Stop()
	t.Service.RestoreLimits()

	for _, r := range t.bufferReaders {
		if r != nil {
			r.Close()
		}
	}
	t.bufferReaders = map[string]*FileReader{}
}

func (t *Torrent) dbidEvent() {
	t.dbidTries++

	if t.DBID != 0 {
		t.dbidTicker.Stop()
		return
	}

	playerID := xbmc.PlayerGetActive()
	if playerID == -1 {
		return
	}

	if item := xbmc.PlayerGetItem(playerID); item != nil {
		t.DBID = item.Info.ID
		t.DBTYPE = item.Info.Type

		t.dbidTicker.Stop()
	} else if t.dbidTries == 10 {
		t.dbidTicker.Stop()
	}
}

func (t *Torrent) seedTickerEvent() {
	log.Debugf("Stopping seeding for: %s", t.Info().Name)
	t.Torrent.SetMaxEstablishedConns(0)
	t.IsSeeding = false
	t.seedTicker.Stop()
}

// Buffer defines buffer pieces for downloading prior to sending file to Kodi.
// Kodi sends two requests, one for onecoming file read handler,
// another for a piece of file from the end (probably to get codec descriptors and so on)
// We set it as post-buffer and include in required buffer pieces array.
func (t *Torrent) Buffer(file *gotorrent.File) {
	if file == nil {
		return
	}

	preBufferStart, preBufferEnd, preBufferOffset, preBufferSize := t.getBufferSize(file, 0, t.Service.GetBufferSize())
	postBufferStart, postBufferEnd, postBufferOffset, postBufferSize := t.getBufferSize(file, file.Length()-endBufferSize, endBufferSize)

	t.muBuffer.Lock()
	t.IsBuffering = true
	t.BufferProgress = 0
	t.BufferLength = preBufferSize + postBufferSize

	for i := preBufferStart; i <= preBufferEnd; i++ {
		t.BufferPiecesProgress[int(i)] = 0
	}
	for i := postBufferStart; i <= postBufferEnd; i++ {
		t.BufferPiecesProgress[int(i)] = 0
		t.BufferEndPieces = append(t.BufferEndPieces, int(i))
	}

	t.muBuffer.Unlock()

	log.Debugf("Setting buffer for file: %s (%#v / %#v). Desired: %#v. Pieces: %#v-%#v + %#v-%#v, Length: %#v / %#v / %#v, Offset: %#v / %#v (%#v)", file.DisplayPath(), file.Length(), file.Torrent().Length(), t.Service.GetBufferSize(), preBufferStart, preBufferEnd, postBufferStart, postBufferEnd, file.Torrent().Info().PieceLength, preBufferSize, postBufferSize, preBufferOffset, postBufferOffset, file.Offset())

	t.Service.SetBufferingLimits()

	t.bufferReaders["pre"], _ = NewFileReader(t, file, "")
	t.bufferReaders["pre"].Seek(preBufferOffset, io.SeekStart)
	t.bufferReaders["pre"].SetReadahead(preBufferSize)

	t.bufferReaders["post"], _ = NewFileReader(t, file, "")
	t.bufferReaders["post"].Seek(postBufferOffset, io.SeekStart)
	t.bufferReaders["post"].SetReadahead(postBufferSize)

	// pieceLength := file.Torrent().Info().PieceLength
	// bufferPieces := int64(math.Ceil(float64(t.Service.GetBufferSize()) / float64(pieceLength)))
	//
	// startPiece, endPiece, _ := t.getFilePiecesAndOffset(file)
	//
	// endBufferPiece := startPiece + bufferPieces - 1
	// endBufferLength := bufferPieces * int64(pieceLength)
	//
	// // postBufferPiece, _ := t.pieceFromOffset(file.Offset() + file.Length() - 5*1024*1024)
	// postBufferPiece, _ := t.pieceFromOffset(file.Offset() + file.Length() - endBufferSize)
	// // postBufferOffset := postBufferPiece * int64(pieceLength)
	// postBufferLength := (endPiece - postBufferPiece + 1) * int64(pieceLength)
	//
	// t.muBuffer.Lock()
	// t.IsBuffering = true
	// t.BufferProgress = 0
	// t.BufferLength = endBufferLength + postBufferLength
	//
	// for i := startPiece; i <= endBufferPiece; i++ {
	// 	t.BufferPiecesProgress[int(i)] = 0
	// }
	// for i := postBufferPiece; i <= endPiece; i++ {
	// 	t.BufferPiecesProgress[int(i)] = 0
	// }
	//
	// // if t.Service.GetStorageType() == StorageMemory {
	// // 	log.Debug("Sending event for initializing memory storage")
	// // 	t.Service.bufferEvents <- len(t.BufferPiecesProgress)
	// // }
	//
	// t.muBuffer.Unlock()
	//
	// log.Debugf("Setting buffer for file: %s. Desired: %#v. Pieces: %#v-%#v + %#v-%#v, Length: %#v / %#v / %#v, Offset: %#v ", file.DisplayPath(), t.Service.GetBufferSize(), startPiece, endBufferPiece, postBufferPiece, endPiece, pieceLength, endBufferLength, postBufferLength, file.Offset())
	//
	// t.Service.SetBufferingLimits()
	//
	// t.bufferReader = t.NewReader(file, false)
	// t.bufferReader.Seek(file.Offset(), io.SeekStart)
	// t.bufferReader.SetReadahead(endBufferLength)
	// // t.bufferReader.Seek(file.Offset(), os.SEEK_SET)
	//
	// t.postReader = t.NewReader(file, false)
	// t.postReader.Seek(file.Offset()+file.Length()-postBufferLength, io.SeekStart)
	// t.postReader.SetReadahead(postBufferLength)
	// // t.postReader.Seek(file.Offset()+file.Length()-postBufferLength, os.SEEK_SET)
}

func (t *Torrent) getBufferSize(f *gotorrent.File, off, length int64) (startPiece, endPiece int, offset, size int64) {
	if off < 0 {
		off = 0
	}

	pieceLength := int64(t.Info().PieceLength)

	offsetStart := f.Offset() + off
	startPiece = int(offsetStart / pieceLength)
	pieceOffset := offsetStart % pieceLength
	offset = offsetStart - pieceOffset

	offsetEnd := offsetStart + length
	pieceOffsetEnd := offsetEnd % pieceLength
	endPiece = int(float64(offsetEnd) / float64(pieceLength))
	if pieceOffsetEnd == 0 {
		endPiece--
	}
	if endPiece >= t.Torrent.NumPieces() {
		endPiece = t.Torrent.NumPieces() - 1
	}

	size = int64(endPiece-startPiece+1) * pieceLength

	// Calculated offset is more than we have in torrent, so correcting the size
	if t.Torrent.Length() != 0 && offset+size >= t.Torrent.Length() {
		size = t.Torrent.Length() - offset
	}

	offset -= f.Offset()
	if offset < 0 {
		offset = 0
	}
	return
}

// GetState ...
func (t *Torrent) GetState() int {
	// log.Debugf("Status: %#v, %#v, %#v, %#v ", t.IsBuffering, t.BytesCompleted(), t.BytesMissing(), t.Stats())

	if t.IsBuffering {
		return StatusBuffering
	}

	havePartial := false
	// log.Debugf("States: %#v", t.PieceStateRuns())
	for _, state := range t.PieceStateRuns() {
		if state.Length == 0 {
			continue
		}

		if state.Checking == true {
			return StatusChecking
		} else if state.Partial == true {
			havePartial = true
		}
	}

	progress := t.GetProgress()
	if progress == 0 {
		return StatusQueued
	} else if progress < 100 {
		if havePartial {
			return StatusDownloading
		} else if t.BytesCompleted() == 0 {
			return StatusQueued
		}
	} else {
		if t.IsSeeding {
			return StatusSeeding
		}
		return StatusFinished
	}

	return StatusQueued
}

// GetStateString ...
func (t *Torrent) GetStateString() string {
	return StatusStrings[t.GetState()]
}

// GetBufferProgress ...
func (t *Torrent) GetBufferProgress() float64 {
	progress := t.BufferProgress
	state := t.GetState()

	if state == StatusChecking {
		total := 0
		checking := 0

		for _, state := range t.PieceStateRuns() {
			if state.Length == 0 {
				continue
			}

			total += state.Length
			if state.Checking == true {
				checking += state.Length
			}
		}

		log.Debugf("Buffer status checking: %#v -- %#v, == %#v", checking, total, progress)
		if total > 0 {
			progress = float64(total-checking) / float64(total) * 100
		}
	}

	if progress > 100 {
		progress = 100
	}

	return progress
}

// GetProgress ...
func (t *Torrent) GetProgress() float64 {
	if t == nil {
		return 0
	}

	var total int64
	for _, f := range t.ChosenFiles {
		total += f.Length()
	}

	if total == 0 {
		return 0
	}

	progress := float64(t.BytesCompleted()) / float64(total) * 100.0
	if progress > 100 {
		progress = 100
	}

	return progress
}

// DownloadFile ...
func (t *Torrent) DownloadFile(f *gotorrent.File) {
	t.ChosenFiles = append(t.ChosenFiles, f)
	log.Debugf("Choosing file for download: %s", f.DisplayPath())
	if t.Storage() != nil && t.Service.config.DownloadStorage != estorage.StorageMemory {
		f.Download()
	}
}

// InfoHash ...
func (t *Torrent) InfoHash() string {
	return t.Torrent.InfoHash().HexString()
}

// Name ...
func (t *Torrent) Name() string {
	return t.Torrent.Name()
}

// Drop ...
func (t *Torrent) Drop(removeFiles bool) {
	log.Infof("Dropping torrent: %s", t.Name())
	files := []string{}
	for _, f := range t.Torrent.Files() {
		files = append(files, f.Path())
	}

	t.closing <- struct{}{}

	for _, r := range t.readers {
		if r != nil {
			r.Close()
		}
	}
	ih := t.Torrent.InfoHash()
	t.Torrent.Drop()

	t.Service.PieceCompletion.Detach(ih)

	if removeFiles {
		go func() {
			// Try to delete in N attemps
			// this is because of opened handles on files which silently goes by
			// so we try until rm fails
			for i := 1; i <= 4; i++ {
				left := 0
				for _, f := range files {
					path := filepath.Join(t.Service.ClientConfig.DataDir, f)
					if _, err := os.Stat(path); err == nil {
						log.Infof("Deleting torrent file at %s", path)
						if errRm := os.Remove(path); errRm != nil {
							continue
						}
						left++
					}
				}

				if left > 0 {
					time.Sleep(time.Duration(i) * time.Second)
				} else {
					return
				}
			}
		}()
	}

	if s := t.Storage(); s != nil && t.Service.config.DownloadStorage == estorage.StorageMemory {
		s.Close()
	}
}

// Pause ...
func (t *Torrent) Pause() {
	if t.Torrent != nil {
		t.Torrent.SetMaxEstablishedConns(0)
	}
	t.IsPaused = true
}

// Resume ...
func (t *Torrent) Resume() {
	if t.Torrent != nil {
		t.Torrent.SetMaxEstablishedConns(1000)
	}
	t.IsPaused = false
}

// GetDBID ...
func (t *Torrent) GetDBID() {
	if t.DBID == 0 && t.needDBID == true {
		log.Debugf("Getting DBID for torrent: %s", t.Name())
		t.needDBID = false
		t.dbidTicker = time.NewTicker(10 * time.Second)
	}
}

// GetDBItem ...
func (t *Torrent) GetDBItem() {
	t.DBItem = t.Service.GetDBItem(t.InfoHash())
}

// GetPlayingItem ...
func (t *Torrent) GetPlayingItem() *PlayingItem {
	if t.DBItem == nil {
		return nil
	}

	TMDBID := t.DBItem.ID
	if t.DBItem.Type != movieType {
		TMDBID = t.DBItem.ShowID
	}

	return &PlayingItem{
		DBID:   t.DBID,
		DBTYPE: t.DBTYPE,

		TMDBID:  TMDBID,
		Season:  t.DBItem.Season,
		Episode: t.DBItem.Episode,

		WatchedTime: WatchedTime,
		Duration:    VideoDuration,
	}
}

func average(xs []int64) float64 {
	var total int64
	for _, v := range xs {
		total += v
	}
	return float64(total) / float64(len(xs))
}
