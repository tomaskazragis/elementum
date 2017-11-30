package bittorrent

import (
	"fmt"
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
	"github.com/op/go-logging"
	// "github.com/dustin/go-humanize"
	// "github.com/elgatito/elementum/cloudhole"
	// "github.com/elgatito/elementum/config"
	// "github.com/zeebo/bencode"

	estorage "github.com/elgatito/elementum/storage"
	"github.com/elgatito/elementum/xbmc"
)

var log = logging.MustGetLogger("torrent")

const (
	STATUS_QUEUED = iota
	STATUS_CHECKING
	STATUS_FINDING
	STATUS_PAUSED
	STATUS_BUFFERING
	STATUS_DOWNLOADING
	STATUS_FINISHED
	STATUS_SEEDING
	STATUS_ALLOCATING
	STATUS_STALLED
)

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

type logWriter struct{ *logging.Logger }

func (w logWriter) Write(b []byte) (int, error) {
	w.Debugf("%s", b)
	return len(b), nil
}

type Torrent struct {
	*gotorrent.Torrent

	readers       map[int]*Reader
	bufferReaders map[string]*Reader

	ChosenFiles []*gotorrent.File
	TorrentPath string

	Storage      estorage.ElementumStorage
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

	downRates      []int64
	upRates        []int64
	rateCounter    int
	downloadedSize int64
	uploadedSize   int64
	dbidTries      int

	pieceLength float64
	// pieceChange *pubsub.Subscription

	closing        chan struct{}
	bufferFinished chan struct{}

	progressTicker *time.Ticker
	bufferTicker   *time.Ticker
	seedTicker     *time.Ticker
	dbidTicker     *time.Ticker
}

func NewTorrent(service *BTService, handle *gotorrent.Torrent, path string) *Torrent {
	t := &Torrent{
		Storage:     service.DefaultStorage,
		Service:     service,
		Torrent:     handle,
		TorrentPath: path,

		bufferReaders:        map[string]*Reader{},
		readers:              map[int]*Reader{},
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

	<-t.GotInfo()

	return t
}

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
	// t.pieceChange = t.Torrent.SubscribePieceStateChanges()

	// defer t.pieceChange.Close()
	defer detailsTicker.Stop()
	defer t.progressTicker.Stop()
	defer t.bufferTicker.Stop()
	defer t.seedTicker.Stop()
	defer t.dbidTicker.Stop()
	defer close(t.bufferFinished)

	for {
		select {
		// case _i, ok := <-t.pieceChange.Values:
		// 	go t.pieceChangeEvent(_i, ok)

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

func (t *Torrent) SeekEvent() {
	if !t.IsPlaying {
		return
	}

	// Closing existing readers, except the last which is a new one
	// for len(t.readers) > 1 {
	// 	t.readers[0].Close()
	// }

	// go t.SyncPieces()
}

func (t *Torrent) CleanupBuffer() {
	for _, v := range t.BufferEndPieces {
		t.Storage.RemovePiece(v)
	}

	t.BufferEndPieces = []int{}
}

// func (t *Torrent) SyncPieces() {
// 	active := map[int]bool{}
//
// 	for i := 0; i < t.Torrent.NumPieces(); i++ {
// 		st := t.Torrent.PieceState(i)
// 		if st.Priority != 0 {
// 			active[i] = true
// 		}
// 	}
//
// 	t.Storage.SyncPieces(active)
// }

// func (t *Torrent) pieceChangeEvent(_i interface{}, ok bool) {
// 	// if !t.IsPlaying || !t.IsOnlyReader || len(t.readers) > 1 || !ok {
// 	// 	return
// 	// }
// 	if !t.IsPlaying || !ok {
// 		return
// 	}
// 	pc := _i.(gotorrent.PieceStateChange)
// 	// if t.IsPlaying {
// 	// 	log.Debugf("PieceChange: %#v == %#v", pc.Index, pc.Priority)
// 	// }
// 	if pc.PieceState.Priority == gotorrent.PiecePriorityNone {
// 		go func() {
// 			t.Storage.RemovePiece(pc.Index)
// 			// t.Torrent.UpdatePieceCompletion(pc.Index)
// 		}()
// 	}
// }

func (t *Torrent) detailsEvent() {
	str := ""
	for i := 0; i < t.Torrent.NumPieces(); i++ {
		st := t.Torrent.PieceState(i)
		if st.Priority > 0 {
			str += fmt.Sprintf("%d:%d  ", i, st.Priority)
		}
	}
	log.Debugf("Priorities: %s  \n", str)
	t.Service.ClientInfo(logWriter{log})
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

	// t.pieceChange.Close()
	t.bufferTicker.Stop()
	t.Service.RestoreLimits()

	for _, r := range t.bufferReaders {
		if r != nil {
			r.Close()
		}
	}
	t.bufferReaders = map[string]*Reader{}
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
		t.DBID = item.Info.Id
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

	log.Debugf("Setting buffer for file: %s (%#v / %#v). Desired: %#v. Pieces: %#v-%#v + %#v-%#v, Length: %#v / %#v / %#v, Offset: %#v / %#v ", file.DisplayPath(), file.Length(), file.Torrent().Length(), t.Service.GetBufferSize(), preBufferStart, preBufferEnd, postBufferStart, postBufferEnd, file.Torrent().Info().PieceLength, preBufferSize, postBufferSize, preBufferOffset, postBufferOffset)

	t.Service.SetBufferingLimits()

	t.bufferReaders["pre"] = t.NewReader(file, false)
	t.bufferReaders["pre"].Seek(preBufferOffset, io.SeekStart)
	t.bufferReaders["pre"].SetReadahead(preBufferSize)

	t.bufferReaders["post"] = t.NewReader(file, false)
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

// func (t *Torrent) pieceFromOffset(offset int64) (int64, int64) {
// 	pieceLength := int64(t.Info().PieceLength)
// 	piece := offset / pieceLength
// 	pieceOffset := offset % pieceLength
// 	return piece, pieceOffset
// }
//
// func (t *Torrent) getFilePiecesAndOffset(f *gotorrent.File) (int64, int64, int64) {
// 	startPiece, offset := t.pieceFromOffset(f.Offset())
// 	endPiece, _ := t.pieceFromOffset(f.Offset() + f.Length())
// 	return startPiece, endPiece, offset
// }
//
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

	return
}

func (t *Torrent) GetState() int {
	// log.Debugf("Status: %#v, %#v, %#v, %#v ", t.IsBuffering, t.BytesCompleted(), t.BytesMissing(), t.Stats())

	if t.IsBuffering {
		return STATUS_BUFFERING
	}

	havePartial := false
	// log.Debugf("States: %#v", t.PieceStateRuns())
	for _, state := range t.PieceStateRuns() {
		if state.Length == 0 {
			continue
		}

		if state.Checking == true {
			return STATUS_CHECKING
		} else if state.Partial == true {
			havePartial = true
		}
	}

	progress := t.GetProgress()
	if progress == 0 {
		return STATUS_QUEUED
	} else if progress < 100 {
		if havePartial {
			return STATUS_DOWNLOADING
		} else if t.BytesCompleted() == 0 {
			return STATUS_QUEUED
		}
	} else {
		if t.IsSeeding {
			return STATUS_SEEDING
		} else {
			return STATUS_FINISHED
		}
	}

	return STATUS_QUEUED
}

func (t *Torrent) GetStateString() string {
	return StatusStrings[t.GetState()]
}

func (t *Torrent) GetBufferProgress() float64 {
	progress := t.BufferProgress
	state := t.GetState()

	if state == STATUS_CHECKING {
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

func (t *Torrent) DownloadFile(f *gotorrent.File) {
	t.ChosenFiles = append(t.ChosenFiles, f)
	log.Debugf("Choosing file for download: %s", f.DisplayPath())
	log.Debugf("Offset: %#v", f.Offset())
	if t.Service.config.DownloadStorage != estorage.StorageMemory {
		f.Download()
	}
}

func (t *Torrent) InfoHash() string {
	return t.Torrent.InfoHash().HexString()
}

func (t *Torrent) Name() string {
	return t.Torrent.Name()
}

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
	t.Torrent.Drop()

	if removeFiles {
		for _, f := range files {
			path := filepath.Join(t.Service.ClientConfig.DataDir, f)
			if _, err := os.Stat(path); err == nil {
				log.Infof("Deleting torrent file at %s", path)
				os.Remove(path)
			}
		}
	}

	if t.Service.config.DownloadStorage == estorage.StorageMemory {
		t.Storage.Close()
	}
}

func (t *Torrent) Pause() {
	if t.Torrent != nil {
		t.Torrent.SetMaxEstablishedConns(0)
	}
	t.IsPaused = true
}

func (t *Torrent) Resume() {
	if t.Torrent != nil {
		t.Torrent.SetMaxEstablishedConns(1000)
	}
	t.IsPaused = false
}

func (t *Torrent) GetDBID() {
	if t.DBID == 0 && t.needDBID == true {
		log.Debugf("Getting DBID for torrent: %s", t.Name())
		t.needDBID = false
		t.dbidTicker = time.NewTicker(10 * time.Second)
	}
}

func (t *Torrent) GetDBItem() {
	t.DBItem = t.Service.GetDBItem(t.InfoHash())
}

func (t *Torrent) GetPlayingItem() *PlayingItem {
	if t.DBItem == nil {
		return nil
	}

	TMDBID := t.DBItem.ID
	if t.DBItem.Type != "movie" {
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

// func (t *Torrent) CurrentPos(pos int64, f *gotorrent.File) {
// 	// log.Debugf("CurrentPos: %#v, %#v, %#v", pos, t.IsBuffering, t.IsPlaying)
// 	// if t.IsBuffering == false && t.IsPlaying == true {
// 	// 	t.Service.StorageEvents.Publish(qstorage.StorageChange{
// 	// 		InfoHash:   t.InfoHash(),
// 	// 		Pos:        pos,
// 	// 		FileLength: f.Length(),
// 	// 		FileOffset: f.Offset(),
// 	// 	})
// 	// }
// }

func average(xs []int64) float64 {
	var total int64
	for _, v := range xs {
		total += v
	}
	return float64(total) / float64(len(xs))
}
