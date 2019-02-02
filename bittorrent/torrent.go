package bittorrent

import (
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"os"
	"sync"
	"time"

	lt "github.com/ElementumOrg/libtorrent-go"
	"github.com/RoaringBitmap/roaring"
	"github.com/dustin/go-humanize"

	"github.com/elgatito/elementum/database"
)

// Torrent ...
type Torrent struct {
	files          []*File
	th             lt.TorrentHandle
	ti             lt.TorrentInfo
	lastStatus     lt.TorrentStatus
	fastResumeFile string
	torrentFile    string
	partsFile      string

	infoHash     string
	readers      map[int64]*TorrentFSEntry
	readerPieces *roaring.Bitmap
	// bufferReaders map[string]*FileReader

	ChosenFiles []*File
	TorrentPath string

	Service *BTService

	BufferLength         int64
	BufferProgress       float64
	BufferPiecesLength   int64
	BufferPiecesProgress map[int]float64
	BufferEndPieces      []int

	IsPlaying    bool
	IsPaused     bool
	IsBuffering  bool
	IsSeeding    bool
	IsRarArchive bool

	DBItem *database.BTItem

	mu        *sync.Mutex
	muBuffer  *sync.RWMutex
	muReaders *sync.Mutex

	pieceLength float64

	closing        chan struct{}
	bufferFinished chan struct{}

	bufferTicker     *time.Ticker
	prioritizeTicker *time.Ticker
}

// NewTorrent ...
func NewTorrent(service *BTService, handle lt.TorrentHandle, info lt.TorrentInfo, path string) *Torrent {
	shaHash := handle.Status().GetInfoHash().ToString()
	infoHash := hex.EncodeToString([]byte(shaHash))

	t := &Torrent{
		infoHash: infoHash,

		Service:     service,
		files:       []*File{},
		th:          handle,
		ti:          info,
		TorrentPath: path,

		readers:      map[int64]*TorrentFSEntry{},
		readerPieces: roaring.NewBitmap(),

		BufferPiecesProgress: map[int]float64{},
		BufferProgress:       -1,
		BufferEndPieces:      []int{},

		mu:        &sync.Mutex{},
		muBuffer:  &sync.RWMutex{},
		muReaders: &sync.Mutex{},

		closing: make(chan struct{}),
	}

	log.Debugf("Waiting for information fetched for torrent: %#v", infoHash)
	// TODO: Do we need to wait for torrent information fetched?

	return t
}

// Storage ...
func (t *Torrent) Storage() lt.StorageInterface {
	return t.th.GetStorageImpl()
}

// Watch ...
func (t *Torrent) Watch() {
	log.Debug("Starting watch events")

	t.startBufferTicker()
	t.bufferFinished = make(chan struct{}, 5)

	t.prioritizeTicker = time.NewTicker(1 * time.Second)

	defer t.bufferTicker.Stop()
	defer t.prioritizeTicker.Stop()
	defer close(t.bufferFinished)

	for {
		select {
		case <-t.bufferTicker.C:
			go t.bufferTickerEvent()

		case <-t.bufferFinished:
			go t.bufferFinishedEvent()

		case <-t.prioritizeTicker.C:
			go t.PrioritizePieces()

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
	if t.IsBuffering {
		// Making sure current progress is not less then previous
		thisProgress := t.GetBufferProgress()

		log.Debugf("Buffer ticker: %#v, %#v", t.IsBuffering, t.BufferPiecesProgress)

		t.muBuffer.Lock()
		defer t.muBuffer.Unlock()

		// thisProgress := float64(t.BufferPiecesLength-progressCount) / float64(t.BufferPiecesLength) * 100
		if thisProgress > t.BufferProgress {
			t.BufferProgress = thisProgress
		}

		if t.BufferProgress >= 100 {
			t.bufferFinished <- struct{}{}
		}
	}
}

func (t *Torrent) bufferFinishedEvent() {
	t.muBuffer.Lock()
	log.Debugf("Buffer finished: %#v, %#v", t.IsBuffering, t.BufferPiecesProgress)

	t.IsBuffering = false

	t.muBuffer.Unlock()

	t.bufferTicker.Stop()
	t.Service.RestoreLimits()
}

// Buffer defines buffer pieces for downloading prior to sending file to Kodi.
// Kodi sends two requests, one for onecoming file read handler,
// another for a piece of file from the end (probably to get codec descriptors and so on)
// We set it as post-buffer and include in required buffer pieces array.
func (t *Torrent) Buffer(file *File) {
	if file == nil {
		return
	}

	t.startBufferTicker()

	preBufferStart, preBufferEnd, preBufferOffset, preBufferSize := t.getBufferSize(file.Offset, 0, t.Service.GetBufferSize())
	postBufferStart, postBufferEnd, postBufferOffset, postBufferSize := t.getBufferSize(file.Offset, file.Size-endBufferSize, endBufferSize)

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

	t.BufferPiecesLength = 0
	for range t.BufferPiecesProgress {
		t.BufferPiecesLength += int64(t.ti.PieceLength())
	}

	t.muBuffer.Unlock()

	log.Debugf("Setting buffer for file: %s (%s / %s). Desired: %s. Pieces: %#v-%#v + %#v-%#v, PieceLength: %s, Pre: %s, Post: %s, WithOffset: %#v / %#v (%#v)",
		file.Path, humanize.Bytes(uint64(file.Size)), humanize.Bytes(uint64(t.ti.TotalSize())),
		humanize.Bytes(uint64(t.Service.GetBufferSize())),
		preBufferStart, preBufferEnd, postBufferStart, postBufferEnd,
		humanize.Bytes(uint64(t.pieceLength)), humanize.Bytes(uint64(preBufferSize)), humanize.Bytes(uint64(postBufferSize)),
		preBufferOffset, postBufferOffset, file.Offset)

	t.Service.SetBufferingLimits()

	piecesPriorities := lt.NewStdVectorInt()
	defer lt.DeleteStdVectorInt(piecesPriorities)

	t.muBuffer.Lock()
	defer t.muBuffer.Unlock()

	// Properly set the pieces priority vector
	curPiece := 0
	for _ = 0; curPiece < preBufferStart; curPiece++ {
		piecesPriorities.Add(0)
	}
	for _ = 0; curPiece <= preBufferEnd; curPiece++ { // get this part
		piecesPriorities.Add(7)
		t.th.SetPieceDeadline(curPiece, 0, 0)
	}
	for _ = 0; curPiece < postBufferStart; curPiece++ {
		piecesPriorities.Add(0)
	}
	for _ = 0; curPiece <= postBufferEnd; curPiece++ { // get this part
		piecesPriorities.Add(7)
		t.th.SetPieceDeadline(curPiece, 0, 0)
	}
	numPieces := t.ti.NumPieces()
	for _ = 0; curPiece < numPieces; curPiece++ {
		piecesPriorities.Add(0)
	}
	t.th.PrioritizePieces(piecesPriorities)
}

func (t *Torrent) getBufferSize(fileOffset int64, off, length int64) (startPiece, endPiece int, offset, size int64) {
	if off < 0 {
		off = 0
	}

	pieceLength := int64(t.ti.PieceLength())

	offsetStart := fileOffset + off
	startPiece = int(offsetStart / pieceLength)
	pieceOffset := offsetStart % pieceLength
	offset = offsetStart - pieceOffset

	offsetEnd := offsetStart + length
	pieceOffsetEnd := offsetEnd % pieceLength
	endPiece = int(float64(offsetEnd) / float64(pieceLength))

	piecesCount := int(t.ti.NumPieces())
	if pieceOffsetEnd == 0 {
		endPiece--
	}
	if endPiece >= piecesCount {
		endPiece = piecesCount - 1
	}

	size = int64(endPiece-startPiece+1) * pieceLength

	// Calculated offset is more than we have in torrent, so correcting the size
	if t.ti.TotalSize() != 0 && offset+size >= t.ti.TotalSize() {
		size = t.ti.TotalSize() - offset
	}

	offset -= fileOffset
	if offset < 0 {
		offset = 0
	}
	return
}

// PrioritizePieces ...
func (t *Torrent) PrioritizePieces() {
	if t.IsBuffering || t.th == nil {
		return
	}

	log.Debugf("Prioritizing pieces")
	t.muReaders.Lock()
	defer t.muReaders.Unlock()

	t.readerPieces.Clear()
	for _, r := range t.readers {
		pr := r.ReaderPiecesRange()
		log.Debugf("Reader range: %#v", pr)
		if pr.Begin < pr.End {
			t.readerPieces.AddRange(uint64(pr.Begin), uint64(pr.End))
		} else {
			t.readerPieces.AddInt(pr.Begin)
		}
	}

	piecesPriorities := lt.NewStdVectorInt()
	defer lt.DeleteStdVectorInt(piecesPriorities)

	curPiece := 0
	numPieces := t.ti.NumPieces()

	readerVector := lt.NewStdVectorInt()
	defer lt.DeleteStdVectorInt(readerVector)

	reservedVector := lt.NewStdVectorInt()
	defer lt.DeleteStdVectorInt(reservedVector)

	reservedVector.Add(0)

	defaultPriority := 1
	if t.Service.config.DownloadStorage == StorageMemory {
		defaultPriority = 0
	}

	seqPiece := -2
	lastPiece := -2

	for _ = 0; curPiece < numPieces; curPiece++ {
		if t.readerPieces.ContainsInt(curPiece) {
			if curPiece > lastPiece+1 {
				seqPiece = curPiece
				lastPiece = curPiece
			} else {
				lastPiece = curPiece
			}

			priority := max(2, 7-int((lastPiece-seqPiece+2)/3))

			// log.Debugf("Priority: %d with priority: %d", curPiece, priority)

			readerVector.Add(curPiece)
			piecesPriorities.Add(priority)
			// t.th.SetPieceDeadline(curPiece, 0, 0)
			t.th.SetPieceDeadline(curPiece, (7-priority)*250, 0)
		} else {
			piecesPriorities.Add(defaultPriority)
		}
	}

	t.th.PrioritizePieces(piecesPriorities)

	if t.Service.config.DownloadStorage == StorageMemory && t.th != nil {
		f := t.th.GetMemoryStorage().(lt.MemoryStorage)
		f.SetTorrentHandle(t.th)
		f.UpdateReaderPieces(readerVector)
		f.UpdateReservedPieces(reservedVector)
	}
}

// GetStatus ...
func (t *Torrent) GetStatus() lt.TorrentStatus {
	return t.th.Status(uint(lt.TorrentHandleQueryName))
}

// GetState ...
func (t *Torrent) GetState() int {
	return int(t.GetStatus().GetState())
}

// GetStateString ...
func (t *Torrent) GetStateString() string {
	return StatusStrings[t.GetState()]
}

// GetBufferProgress ...
func (t *Torrent) GetBufferProgress() float64 {
	t.BufferProgress = float64(0)
	t.muBuffer.Lock()
	defer t.muBuffer.Unlock()

	if len(t.BufferPiecesProgress) > 0 {
		totalProgress := float64(0)
		t.piecesProgress(t.BufferPiecesProgress)
		for _, v := range t.BufferPiecesProgress {
			totalProgress += v
		}
		t.BufferProgress = 100 * totalProgress / float64(len(t.BufferPiecesProgress))
	}

	if t.BufferProgress > 100 {
		return 100
	}

	return t.BufferProgress
}

func (t *Torrent) piecesProgress(pieces map[int]float64) {
	queue := lt.NewStdVectorPartialPieceInfo()
	defer lt.DeleteStdVectorPartialPieceInfo(queue)

	t.th.GetDownloadQueue(queue)
	for piece := range pieces {
		if t.th.HavePiece(piece) == true {
			pieces[piece] = 1.0
		}
	}
	queueSize := queue.Size()
	for i := 0; i < int(queueSize); i++ {
		ppi := queue.Get(i)
		pieceIndex := ppi.GetPieceIndex()
		if _, exists := pieces[pieceIndex]; exists {
			blocks := ppi.Blocks()
			totalBlocks := ppi.GetBlocksInPiece()
			totalBlockDownloaded := uint(0)
			totalBlockSize := uint(0)
			for j := 0; j < totalBlocks; j++ {
				block := blocks.Getitem(j)
				totalBlockDownloaded += block.GetBytesProgress()
				totalBlockSize += block.GetBlockSize()
			}
			pieces[pieceIndex] = float64(totalBlockDownloaded) / float64(totalBlockSize)
		}
	}
}

// GetProgress ...
func (t *Torrent) GetProgress() float64 {
	if t == nil {
		return 0
	}

	return float64(t.th.Status().GetProgress()) * 100
}

// DownloadFile ...
func (t *Torrent) DownloadFile(addFile *File) {
	t.ChosenFiles = append(t.ChosenFiles, addFile)

	for _, f := range t.files {
		if f != addFile {
			continue
		}

		log.Debugf("Choosing file for download: %s", f.Path)
		t.th.FilePriority(f.Index, 1)
	}
}

// InfoHash ...
func (t *Torrent) InfoHash() string {
	if t.th == nil {
		return ""
	}

	shaHash := t.th.Status().GetInfoHash().ToString()
	return hex.EncodeToString([]byte(shaHash))
}

// Name ...
func (t *Torrent) Name() string {
	if t.th == nil {
		return ""
	}

	return t.ti.Name()
}

// Length ...
func (t *Torrent) Length() int64 {
	if t.th == nil {
		return 0
	}

	return t.ti.TotalSize()
}

// Drop ...
func (t *Torrent) Drop(removeFiles bool) {
	log.Infof("Dropping torrent: %s", t.Name())
	t.closing <- struct{}{}

	for _, r := range t.readers {
		if r != nil {
			r.Close()
		}
	}

	go func() {
		toRemove := 0
		if removeFiles {
			toRemove = 1
		}

		t.Service.Session.GetHandle().RemoveTorrent(t.th, toRemove)
	}()
}

// Pause ...
func (t *Torrent) Pause() {
	t.th.Pause()

	t.IsPaused = true
}

// Resume ...
func (t *Torrent) Resume() {
	t.th.Resume()

	t.IsPaused = false
}

// GetDBItem ...
func (t *Torrent) GetDBItem() *database.BTItem {
	return t.DBItem
}

// SaveMetainfo ...
func (t *Torrent) SaveMetainfo(path string) error {
	// Not saving torrent for memory storage
	if t.Service.config.DownloadStorage == StorageMemory {
		return nil
	}
	if t.th == nil {
		return fmt.Errorf("Torrent is not available")
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("Directory %s does not exist", path)
	}

	bEncodedTorrent := t.GetMetadata()
	ioutil.WriteFile(path, bEncodedTorrent, 0644)

	return nil
}

// GetReadaheadSize ...
func (t *Torrent) GetReadaheadSize() (ret int64) {
	defer func() {
		log.Debugf("Readahead size: %s", humanize.Bytes(uint64(ret)))
	}()

	defaultRA := int64(50 * 1024 * 1024)
	if t.Service.config.DownloadStorage != StorageMemory {
		return defaultRA
	}

	size := defaultRA
	if t.Storage() != nil && len(t.readers) > 0 {
		size = lt.GetMemorySize()
	}
	if size < 0 {
		return 0
	}

	return int64(float64(size-int64(3*t.ti.PieceLength())) * (1 / float64(len(t.readers))))
}

// CloseReaders ...
func (t *Torrent) CloseReaders() {
	t.muReaders.Lock()
	defer t.muReaders.Unlock()

	for k, r := range t.readers {
		log.Debugf("Closing active reader: %d", r.id)
		r.Close()
		delete(t.readers, k)
	}
}

// ResetReaders ...
func (t *Torrent) ResetReaders() {
	t.muReaders.Lock()
	defer t.muReaders.Unlock()

	if len(t.readers) == 0 {
		return
	}

	perReaderSize := t.GetReadaheadSize()
	for _, r := range t.readers {
		log.Infof("Setting readahead for reader %d as %s", r.id, humanize.Bytes(uint64(perReaderSize)))
		r.readahead = perReaderSize
	}
}

// GetMetadata ...
func (t *Torrent) GetMetadata() []byte {
	torrentFile := lt.NewCreateTorrent(t.ti)
	defer lt.DeleteCreateTorrent(torrentFile)
	torrentContent := torrentFile.Generate()
	return []byte(lt.Bencode(torrentContent))
}

// MakeFiles ...
func (t *Torrent) MakeFiles() {
	numFiles := t.ti.NumFiles()
	files := t.ti.Files()

	for i := 0; i < numFiles; i++ {
		t.files = append(t.files, &File{
			Index:  i,
			Name:   files.FileName(i),
			Size:   files.FileSize(i),
			Offset: files.FileOffset(i),
			Path:   files.FilePath(i),
		})
	}
}

func average(xs []int64) float64 {
	var total int64
	for _, v := range xs {
		total += v
	}
	return float64(total) / float64(len(xs))
}
