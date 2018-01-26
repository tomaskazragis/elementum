package bittorrent

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"time"

	gotorrent "github.com/anacrolix/torrent"

	"github.com/elgatito/elementum/database"
	estorage "github.com/elgatito/elementum/storage"
)

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
	// IsDownloadStarted used to mark started downloads to avoid getting
	// "Checked" status when one piece is in checking state
	IsDownloadStarted bool

	IsRarArchive bool

	needSeeding bool

	DBItem *database.BTItem

	mu        *sync.Mutex
	muBuffer  *sync.RWMutex
	muSeeding *sync.RWMutex

	downRates      []int64
	upRates        []int64
	rateCounter    int
	downloadedSize int64
	uploadedSize   int64
	lastProgress   float64

	pieceLength float64

	closing        chan struct{}
	bufferFinished chan struct{}

	progressTicker *time.Ticker
	bufferTicker   *time.Ticker
	seedTicker     *time.Ticker
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

		mu:        &sync.Mutex{},
		muBuffer:  &sync.RWMutex{},
		muSeeding: &sync.RWMutex{},

		closing: make(chan struct{}),

		seedTicker: &time.Ticker{},
	}

	log.Debugf("Waiting for information fetched for torrent: %#v", handle.InfoHash().HexString())
	<-t.GotInfo()
	log.Debugf("Information fetched for torrent: %#v", handle.InfoHash().HexString())

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

	t.progressTicker = time.NewTicker(1 * time.Second)

	t.startBufferTicker()
	t.bufferFinished = make(chan struct{}, 5)

	t.downRates = []int64{0, 0, 0, 0, 0}
	t.upRates = []int64{0, 0, 0, 0, 0}
	t.rateCounter = 0
	t.downloadedSize = 0
	t.uploadedSize = 0

	t.pieceLength = float64(t.Torrent.Info().PieceLength)

	defer t.progressTicker.Stop()
	defer t.bufferTicker.Stop()
	defer t.seedTicker.Stop()
	defer close(t.bufferFinished)

	for {
		select {
		case <-t.bufferTicker.C:
			go t.bufferTickerEvent()

		case <-t.bufferFinished:
			go t.bufferFinishedEvent()

		case <-t.progressTicker.C:
			go t.progressEvent()

		case <-t.seedTicker.C:
			go t.seedTickerEvent()

		case <-t.closing:
			log.Debug("Stopping watch events")
			return
		}
	}
}

func (t *Torrent) startBufferTicker() {
	t.bufferTicker = time.NewTicker(1 * time.Second)
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

		total := float64(len(t.BufferPiecesProgress)) * t.pieceLength
		// Making sure current progress is not less then previous
		thisProgress := (total - progressCount) / total * 100
		if thisProgress > t.BufferProgress {
			t.BufferProgress = thisProgress
		}

		if t.BufferProgress >= 100 {
			t.bufferFinished <- struct{}{}
		}
	}

	t.muBuffer.Unlock()
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

	curDownloaded := t.Torrent.BytesCompleted()
	curUploaded := t.Torrent.Stats().DataBytesWritten
	t.downRates[t.rateCounter] = curDownloaded - t.downloadedSize
	t.upRates[t.rateCounter] = curUploaded - t.uploadedSize

	if curDownloaded > t.downloadedSize {
		t.downloadedSize = curDownloaded
	}
	if curUploaded > t.uploadedSize {
		t.uploadedSize = curUploaded
	}

	t.DownloadRate = int64(average(t.downRates))
	if t.DownloadRate < 0 {
		t.DownloadRate = 0
	}
	t.UploadRate = int64(average(t.upRates))
	if t.UploadRate < 0 {
		t.UploadRate = 0
	}

	t.rateCounter++
	if t.rateCounter == len(t.downRates)-1 {
		t.rateCounter = 0
	}

	// if t.lastDownRate == t.downloadedSize {
	// 	buf := bytes.NewBuffer([]byte{})
	// 	t.Service.ClientInfo(buf)
	// 	log.Debug("Download stale. Client state:\n", buf.String())
	// }
	// t.lastDownRate = t.downloadedSize

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

	t.startBufferTicker()

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

		if !t.IsDownloadStarted && state.Checking == true {
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
			t.IsDownloadStarted = true
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
	} else if progress < t.lastProgress {
		progress = t.lastProgress
	}

	// TODO: replace with proper Rate calculator
	// when it's available in the library
	t.lastProgress = progress
	return progress
}

// DownloadFile ...
func (t *Torrent) DownloadFile(f *gotorrent.File) {
	t.ChosenFiles = append(t.ChosenFiles, f)
	log.Debugf("Choosing file for download: %s", f.DisplayPath())
	// TODO: Change this in general to be able to use per-torrent storage
	if t.Storage() != nil && t.Service.config.DownloadStorage != estorage.StorageMemory {
		f.Download()
	}
}

// InfoHash ...
func (t *Torrent) InfoHash() string {
	if t.Torrent == nil {
		return ""
	}

	return t.Torrent.InfoHash().HexString()
}

// Name ...
func (t *Torrent) Name() string {
	if t.Torrent == nil {
		return ""
	}

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
	t.Torrent.Drop()

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

// GetDBItem ...
func (t *Torrent) GetDBItem() *database.BTItem {
	return t.DBItem
}

// SaveMetainfo ...
func (t *Torrent) SaveMetainfo(path string) error {
	// Not saving torrent for memory storage
	if t.Service.config.DownloadStorage == estorage.StorageMemory {
		return nil
	}
	if t.Torrent == nil {
		return fmt.Errorf("Torrent is not available")
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("Directory %s does not exist", path)
	}

	var buf bytes.Buffer
	t.Torrent.Metainfo().Write(&buf)

	filePath := filepath.Join(path, fmt.Sprintf("%s.torrent", t.InfoHash()))
	log.Debugf("Saving torrent to %s", filePath)
	if err := ioutil.WriteFile(filePath, buf.Bytes(), 0644); err != nil {
		return err
	}

	return nil
}

func average(xs []int64) float64 {
	var total int64
	for _, v := range xs {
		total += v
	}
	return float64(total) / float64(len(xs))
}
