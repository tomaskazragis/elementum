package bittorrent

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io/ioutil"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
	"unsafe"

	lt "github.com/ElementumOrg/libtorrent-go"
	"github.com/RoaringBitmap/roaring"
	"github.com/anacrolix/missinggo"
	"github.com/anacrolix/missinggo/perf"
	"github.com/dustin/go-humanize"

	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/database"
	"github.com/elgatito/elementum/util"
)

// Torrent ...
type Torrent struct {
	files          []*File
	th             lt.TorrentHandle
	ti             lt.TorrentInfo
	lastStatus     lt.TorrentStatus
	ms             lt.MemoryStorage
	fastResumeFile string
	torrentFile    string
	partsFile      string

	name           string
	infoHash       string
	readers        map[int64]*TorrentFSEntry
	reservedPieces []int
	awaitingPieces *roaring.Bitmap
	// bufferReaders map[string]*FileReader

	ChosenFiles []*File
	TorrentPath string

	Service *Service

	BufferLength         int64
	BufferProgress       float64
	BufferPiecesLength   int64
	BufferPiecesProgress map[int]float64
	BufferEndPieces      []int
	MemorySize           int64

	IsPlaying    bool
	IsPaused     bool
	IsBuffering  bool
	IsSeeding    bool
	IsRarArchive bool

	DBItem *database.BTItem

	mu        *sync.Mutex
	muBuffer  *sync.RWMutex
	muReaders *sync.Mutex

	pieceLength int64

	gotMetainfo    missinggo.Event
	Closer         missinggo.Event
	bufferFinished chan struct{}

	piecesMx          sync.RWMutex
	pieces            Bitfield
	piecesLastUpdated time.Time

	bufferTicker     *time.Ticker
	prioritizeTicker *time.Ticker
}

// NewTorrent ...
func NewTorrent(service *Service, handle lt.TorrentHandle, info lt.TorrentInfo, path string) *Torrent {
	shaHash := handle.Status().GetInfoHash().ToString()
	infoHash := hex.EncodeToString([]byte(shaHash))

	t := &Torrent{
		infoHash: infoHash,

		Service:     service,
		files:       []*File{},
		th:          handle,
		ti:          info,
		TorrentPath: path,

		readers:        map[int64]*TorrentFSEntry{},
		reservedPieces: []int{},
		awaitingPieces: roaring.NewBitmap(),
		// readerPieces: roaring.NewBitmap(),

		BufferPiecesProgress: map[int]float64{},
		BufferProgress:       -1,
		BufferEndPieces:      []int{},

		mu:        &sync.Mutex{},
		muBuffer:  &sync.RWMutex{},
		muReaders: &sync.Mutex{},
	}

	return t
}

// GotInfo ...
func (t *Torrent) GotInfo() <-chan struct{} {
	return t.gotMetainfo.C()
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
	readersTicker := time.NewTicker(10 * time.Second)

	sc := t.Service.Closer.C()
	tc := t.Closer.C()

	defer t.bufferTicker.Stop()
	defer t.prioritizeTicker.Stop()
	defer readersTicker.Stop()
	defer close(t.bufferFinished)

	for {
		select {
		case <-t.bufferTicker.C:
			go t.bufferTickerEvent()

		case <-t.bufferFinished:
			go t.bufferFinishedEvent()

		case <-t.prioritizeTicker.C:
			go t.PrioritizePieces()

		case <-readersTicker.C:
			go t.ResetReaders()
		case <-sc:
			t.Closer.Set()
			return

		case <-tc:
			log.Debug("Stopping watch events")
			return
		}
	}
}

func (t *Torrent) startBufferTicker() {
	t.bufferTicker = time.NewTicker(1 * time.Second)
}

func (t *Torrent) bufferTickerEvent() {
	defer perf.ScopeTimer()()

	if t.IsBuffering && len(t.BufferPiecesProgress) > 0 {
		// Making sure current progress is not less then previous
		thisProgress := t.GetBufferProgress()

		piecesStatus := "["
		piecesKeys := []int{}
		for k := range t.BufferPiecesProgress {
			piecesKeys = append(piecesKeys, k)
		}
		sort.Ints(piecesKeys)

		for _, k := range piecesKeys {
			piecesStatus += fmt.Sprintf("%d:%d, ", k, int(t.BufferPiecesProgress[k]*100))
		}

		if len(piecesStatus) > 1 {
			piecesStatus = piecesStatus[0:len(piecesStatus)-2] + "]"
		}

		downSpeed, upSpeed := t.GetHumanizedSpeeds()
		log.Debugf("Buffer. Pr: %d%%, Sp: %s / %s, Pi: %s", int(thisProgress), downSpeed, upSpeed, piecesStatus)

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

// GetSpeeds returns download and upload speeds
func (t *Torrent) GetSpeeds() (down, up int) {
	torrentStatus := t.th.Status(uint(lt.TorrentHandleQueryName))
	return torrentStatus.GetDownloadPayloadRate(), torrentStatus.GetUploadPayloadRate()
}

// GetHumanizedSpeeds returns humanize download and upload speeds
func (t *Torrent) GetHumanizedSpeeds() (down, up string) {
	downInt, upInt := t.GetSpeeds()
	return humanize.Bytes(uint64(downInt)), humanize.Bytes(uint64(upInt))
}

func (t *Torrent) bufferFinishedEvent() {
	t.muBuffer.Lock()
	log.Debugf("Buffer finished: %#v, %#v", t.IsBuffering, t.BufferPiecesProgress)

	t.BufferPiecesProgress = map[int]float64{}
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

	startBufferSize := t.Service.GetBufferSize()
	preBufferStart, preBufferEnd, preBufferOffset, preBufferSize := t.getBufferSize(file.Offset, 0, startBufferSize)
	postBufferStart, postBufferEnd, postBufferOffset, postBufferSize := t.getBufferSize(file.Offset, file.Size-EndBufferSize, EndBufferSize)

	if config.Get().AutoAdjustBufferSize && preBufferEnd-preBufferStart < 10 {
		_, free := t.Service.GetMemoryStats()

		// Let's try to
		var newBufferSize int64
		for i := 10; i >= 4; i -= 2 {
			mem := int64(i) * t.pieceLength

			if mem < startBufferSize {
				break
			}
			if free == 0 || !t.Service.IsMemoryStorage() || (mem*2)-startBufferSize < free {
				newBufferSize = mem
				break
			}
		}

		if newBufferSize > 0 {
			startBufferSize = newBufferSize
			preBufferStart, preBufferEnd, preBufferOffset, preBufferSize = t.getBufferSize(file.Offset, 0, startBufferSize)
			log.Infof("Adjusting buffer size to %s, to have at least %d pieces ready!", humanize.Bytes(uint64(startBufferSize)), preBufferEnd-preBufferStart+1)
		}
	}

	if t.Service.IsMemoryStorage() {
		t.ms = t.th.GetMemoryStorage().(lt.MemoryStorage)
		t.ms.SetTorrentHandle(t.th)

		// Try to increase memory size to at most 25 pieces to have more comfortable playback.
		// Also check for free memory to avoid spending too much!
		if config.Get().AutoAdjustMemorySize {
			_, free := t.Service.GetMemoryStats()

			var newMemorySize int64
			for i := 25; i >= 10; i -= 5 {
				mem := int64(i) * t.pieceLength

				if mem < t.MemorySize || free == 0 {
					break
				}
				if (mem*2)-t.MemorySize < free {
					newMemorySize = mem
					break
				}
			}

			if newMemorySize > 0 {
				t.MemorySize = newMemorySize
				log.Infof("Adjusting memory size to %s!", humanize.Bytes(uint64(t.MemorySize)))
				t.ms.SetMemorySize(t.MemorySize)
			}
		}

		// Increase memory size if buffer does not fit there
		if preBufferSize+postBufferSize > t.MemorySize {
			t.MemorySize = preBufferSize + postBufferSize + (1 * t.pieceLength)
			log.Infof("Adjusting memory size to %s, to fit all buffer!", humanize.Bytes(uint64(t.MemorySize)))
			t.ms.SetMemorySize(t.MemorySize)
		}
	}

	t.muBuffer.Lock()
	t.IsBuffering = true
	t.BufferProgress = 0
	t.BufferLength = preBufferSize + postBufferSize

	for i := preBufferStart; i <= preBufferEnd; i++ {
		t.BufferPiecesProgress[i] = 0
	}
	for i := postBufferStart; i <= postBufferEnd; i++ {
		t.BufferPiecesProgress[i] = 0
		t.BufferEndPieces = append(t.BufferEndPieces, i)
	}

	t.BufferPiecesLength = 0
	for range t.BufferPiecesProgress {
		t.BufferPiecesLength += t.pieceLength
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

	// Let's try without reserving pieces
	// Reserving first piece in this file, it is usually re-requested by Kodi
	// t.reservedPieces = append(t.reservedPieces, preBufferStart, postBufferEnd)

	// Properly set the pieces priority vector
	curPiece := 0
	defaultPriority := 0
	if !t.Service.IsMemoryStorage() {
		defaultPriority = 1
	}

	for _ = 0; curPiece < preBufferStart; curPiece++ {
		piecesPriorities.Add(defaultPriority)
	}
	for _ = 0; curPiece <= preBufferEnd; curPiece++ { // get this part
		piecesPriorities.Add(7)
		t.th.SetPieceDeadline(curPiece, 0, 0)
	}
	for _ = 0; curPiece < postBufferStart; curPiece++ {
		piecesPriorities.Add(defaultPriority)
	}
	for _ = 0; curPiece <= postBufferEnd; curPiece++ { // get this part
		piecesPriorities.Add(7)
		t.th.SetPieceDeadline(curPiece, 0, 0)
	}
	numPieces := t.ti.NumPieces()
	for _ = 0; curPiece < numPieces; curPiece++ {
		piecesPriorities.Add(defaultPriority)
	}
	t.th.PrioritizePieces(piecesPriorities)

	// Resuming in case it was paused by anyone
	t.Resume()
}

func (t *Torrent) getBufferSize(fileOffset int64, off, length int64) (startPiece, endPiece int, offset, size int64) {
	if off < 0 {
		off = 0
	}

	t.pieceLength = int64(t.ti.PieceLength())

	offsetStart := fileOffset + off
	startPiece = int(offsetStart / t.pieceLength)
	pieceOffset := offsetStart % t.pieceLength
	offset = offsetStart - pieceOffset

	offsetEnd := offsetStart + length
	pieceOffsetEnd := offsetEnd % t.pieceLength
	endPiece = int(math.Ceil(float64(offsetEnd) / float64(t.pieceLength)))

	piecesCount := int(t.ti.NumPieces())
	if pieceOffsetEnd == 0 {
		endPiece--
	}
	if endPiece >= piecesCount {
		endPiece = piecesCount - 1
	}

	size = int64(endPiece-startPiece+1) * t.pieceLength

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

// PrioritizePiece ...
func (t *Torrent) PrioritizePiece(piece int) {
	if t.IsBuffering || t.th == nil {
		return
	}

	defer perf.ScopeTimer()()

	t.awaitingPieces.AddInt(piece)

	t.th.PiecePriority(piece, 7)
	t.th.SetPieceDeadline(piece, 0, 0)
}

// PrioritizePieces ...
func (t *Torrent) PrioritizePieces() {
	if t.IsBuffering || t.IsSeeding || !t.IsPlaying || t.th == nil {
		return
	}

	defer perf.ScopeTimer()()

	downSpeed, upSpeed := t.GetHumanizedSpeeds()
	log.Debugf("Prioritizing pieces: %s / %s", downSpeed, upSpeed)

	t.muReaders.Lock()

	readerProgress := map[int]float64{}
	readerPieces := map[int]int{}
	readerKeys := []int64{}
	for k := range t.readers {
		readerKeys = append(readerKeys, k)
	}

	sort.Slice(readerKeys, func(i int, j int) bool {
		return t.readers[readerKeys[i]].lastUsed.Before(t.readers[readerKeys[j]].lastUsed)
	})

	lastBonus := 0
	for _, k := range readerKeys {
		lastBonus++
		readerPriority := len(readerKeys) - lastBonus

		pr := t.readers[k].ReaderPiecesRange()
		log.Debugf("Reader range: %#v, priority: %d", pr, readerPriority)

		for curPiece := pr.Begin; curPiece <= pr.End; curPiece++ {
			// Basically we set priority to each needed piece with this logic:
			//   Starting from 7 (that is the max), then each next piece has
			//   lower priority, plus more up to date reader has bigger priority,
			//   that is because Kodi requests multiple pieces at once, while
			//   not closing old readers, so we try to first download
			//   for most recent reader.
			if t.awaitingPieces.ContainsInt(curPiece) {
				readerPieces[curPiece] = 7
			} else {
				readerPieces[curPiece] = util.Max(2, 7-readerPriority-int((curPiece-pr.Begin+2)/3))
			}
			readerProgress[curPiece] = 0
		}
	}
	t.muReaders.Unlock()

	// Update progress for piece completion
	t.piecesProgress(readerProgress)

	piecesPriorities := lt.NewStdVectorInt()
	defer lt.DeleteStdVectorInt(piecesPriorities)

	readerVector := lt.NewStdVectorInt()
	defer lt.DeleteStdVectorInt(readerVector)

	reservedVector := lt.NewStdVectorInt()
	defer lt.DeleteStdVectorInt(reservedVector)

	piecesStatus := "["
	piecesKeys := []int{}
	for k := range readerPieces {
		piecesKeys = append(piecesKeys, k)
	}
	sort.Ints(piecesKeys)

	for _, k := range piecesKeys {
		readerVector.Add(k)

		if readerPieces[k] > 0 {
			piecesStatus += fmt.Sprintf("%d:%d:%d, ", k, readerPieces[k], int(readerProgress[k]*100))
		}
	}

	if len(piecesStatus) > 1 {
		piecesStatus = piecesStatus[0:len(piecesStatus)-2] + "]"
		log.Debugf("Priorities: %s", piecesStatus)
	}

	numPieces := t.ti.NumPieces()

	for _, piece := range t.reservedPieces {
		reservedVector.Add(piece)
	}

	if t.Service.IsMemoryStorage() && t.th != nil && t.ms != nil {
		t.ms.UpdateReaderPieces(readerVector)
		t.ms.UpdateReservedPieces(reservedVector)
	}

	defaultPriority := 1
	if t.Service.IsMemoryStorage() {
		defaultPriority = 0
	}

	// Splitting to first set priorities, then deadlines,
	// so that latest set deadline is the nearest piece number
	for curPiece := 0; curPiece < numPieces; curPiece++ {
		if priority, ok := readerPieces[curPiece]; ok {
			readerVector.Add(curPiece)
			piecesPriorities.Add(priority)
			// t.th.SetPieceDeadline(curPiece, 0, 0)
			// t.th.SetPieceDeadline(curPiece, (7-priority)*20, 0)
		} else {
			piecesPriorities.Add(defaultPriority)
			// if defaultPriority == 0 {
			// 	t.th.ResetPieceDeadline(curPiece)
			// }
		}
	}

	for curPiece := numPieces - 1; curPiece >= 0; curPiece-- {
		if priority, ok := readerPieces[curPiece]; ok {
			t.th.SetPieceDeadline(curPiece, (7-priority)*10, 0)
		}
	}

	// for curPiece, priority := range readerPieces {
	// 	// if priority >= 5 {
	// 	t.th.SetPieceDeadline(curPiece, (7-priority)*10, 0)
	// 	// }
	// }

	t.th.PrioritizePieces(piecesPriorities)
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
	if t.Service.IsMemoryStorage() {
		if t.IsBuffering {
			return StatusStrings[StatusBuffering]
		} else if t.IsPlaying {
			return StatusStrings[StatusPlaying]
		}
	}

	torrentStatus := t.th.Status()
	progress := float64(torrentStatus.GetProgress()) * 100
	state := t.GetState()

	if t.Service.Session.GetHandle().IsPaused() {
		return StatusStrings[StatusPaused]
	} else if torrentStatus.GetPaused() && state != StatusFinished && state != StatusFinding {
		if progress == 100 {
			return StatusStrings[StatusFinished]
		}

		return StatusStrings[StatusPaused]
	} else if !torrentStatus.GetPaused() && (state == StatusFinished || progress == 100) {
		return StatusStrings[StatusSeeding]
	} else if state != StatusQueued && t.IsBuffering {
		return StatusStrings[StatusBuffering]
	}

	return StatusStrings[state]
}

// GetBufferProgress ...
func (t *Torrent) GetBufferProgress() float64 {
	defer perf.ScopeTimer()()

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
	defer perf.ScopeTimer()()

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

	// For memory storage let's show playback progress,
	// because we can't know real progress of download
	if t.Service.IsMemoryStorage() {
		if player := t.Service.GetActivePlayer(); player != nil && player.p.VideoDuration != 0 {
			return player.p.WatchedTime / player.p.VideoDuration * 100
		}
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
	if t.name != "" {
		return t.name
	}

	if t.th == nil {
		return ""
	}

	t.name = t.ti.Name()
	return t.name
}

// Length ...
func (t *Torrent) Length() int64 {
	if t.th == nil || !t.gotMetainfo.IsSet() {
		return 0
	}

	return t.ti.TotalSize()
}

// Drop ...
func (t *Torrent) Drop(removeFiles bool) {
	defer perf.ScopeTimer()()

	log.Infof("Dropping torrent: %s", t.Name())
	t.Closer.Set()

	for _, r := range t.readers {
		if r != nil {
			r.Close()
		}
	}

	// Removing in background to avoid blocking UI
	go func() {
		toRemove := 0
		if removeFiles {
			toRemove = 1
		}

		t.Service.Session.GetHandle().RemoveTorrent(t.th, toRemove)

		// Removing .torrent file
		if _, err := os.Stat(t.torrentFile); err == nil {
			log.Infof("Deleting torrent file at %s", t.torrentFile)
			defer os.Remove(t.torrentFile)
		}

		if removeFiles || t.Service.IsMemoryStorage() {
			// Removing .fastresume file
			if _, err := os.Stat(t.fastResumeFile); err == nil {
				log.Infof("Deleting fast resume data at %s", t.fastResumeFile)
				defer os.Remove(t.fastResumeFile)
			}

			// Removing .parts file
			if _, err := os.Stat(t.partsFile); err == nil {
				log.Infof("Deleting parts file at %s", t.partsFile)
				defer os.Remove(t.partsFile)
			}
		}
	}()
}

// Pause ...
func (t *Torrent) Pause() {
	log.Infof("Pausing torrent: %s", t.InfoHash())
	t.th.Pause()

	t.IsPaused = true
}

// Resume ...
func (t *Torrent) Resume() {
	log.Infof("Resuming torrent: %s", t.InfoHash())
	t.th.Resume()

	t.IsPaused = false
}

// GetDBItem ...
func (t *Torrent) GetDBItem() *database.BTItem {
	return t.DBItem
}

// SaveMetainfo ...
func (t *Torrent) SaveMetainfo(path string) error {
	defer perf.ScopeTimer()()

	// Not saving torrent for memory storage
	if t.Service.IsMemoryStorage() {
		return nil
	}
	if t.th == nil {
		return fmt.Errorf("Torrent is not available")
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("Directory %s does not exist", path)
	}

	path = filepath.Join(path, t.InfoHash()+".torrent")
	bEncodedTorrent := t.GetMetadata()
	ioutil.WriteFile(path, bEncodedTorrent, 0644)

	return nil
}

// GetReadaheadSize ...
func (t *Torrent) GetReadaheadSize() (ret int64) {
	defer perf.ScopeTimer()()

	defer func() {
		log.Debugf("Readahead size: %s", humanize.Bytes(uint64(ret)))
	}()

	defaultRA := int64(50 * 1024 * 1024)
	if !t.Service.IsMemoryStorage() {
		return defaultRA
	}

	size := defaultRA
	if t.Storage() != nil && len(t.readers) > 0 {
		size = lt.GetMemorySize()
	}
	if size < 0 {
		return 0
	}

	log.Debugf("Size from lt: %d, set: %d, res: %d, pl: %d, pl2: %d", size, t.MemorySize, len(t.reservedPieces), t.ti.PieceLength(), t.pieceLength)
	base := float64(t.MemorySize - (int64(len(t.reservedPieces)+1))*t.pieceLength)
	if len(t.readers) == 1 {
		return int64(base)
	}

	return int64(base * 0.5)
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
		size := perReaderSize

		if r.readahead == size {
			continue
		}

		log.Infof("Setting readahead for reader %d as %s", r.id, humanize.Bytes(uint64(size)))
		r.readahead = size
	}
}

// GetMetadata ...
func (t *Torrent) GetMetadata() []byte {
	defer perf.ScopeTimer()()

	torrentFile := lt.NewCreateTorrent(t.ti)
	defer lt.DeleteCreateTorrent(torrentFile)
	torrentContent := torrentFile.Generate()
	return []byte(lt.Bencode(torrentContent))
}

// MakeFiles ...
func (t *Torrent) MakeFiles() {
	numFiles := t.ti.NumFiles()
	files := t.ti.Files()
	t.files = []*File{}

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

func (t *Torrent) updatePieces() error {
	defer perf.ScopeTimer()()

	t.piecesMx.Lock()
	defer t.piecesMx.Unlock()

	if time.Now().After(t.piecesLastUpdated.Add(piecesRefreshDuration)) {
		// need to keep a reference to the status or else the pieces bitfield
		// is at risk of being collected
		t.lastStatus = t.th.Status(uint(lt.TorrentHandleQueryPieces))
		if t.lastStatus.GetState() > lt.TorrentStatusSeeding {
			return errors.New("Torrent file has invalid state")
		}
		piecesBits := t.lastStatus.GetPieces()
		piecesBitsSize := piecesBits.Size()
		piecesSliceSize := piecesBitsSize / 8
		if piecesBitsSize%8 > 0 {
			// Add +1 to round up the bitfield
			piecesSliceSize++
		}
		data := (*[100000000]byte)(unsafe.Pointer(piecesBits.Bytes()))[:piecesSliceSize]
		t.pieces = Bitfield(data)
		t.piecesLastUpdated = time.Now()
	}
	return nil
}

func (t *Torrent) hasPiece(idx int) bool {
	if err := t.updatePieces(); err != nil {
		return false
	}
	t.piecesMx.RLock()
	defer t.piecesMx.RUnlock()
	return t.pieces.GetBit(idx)
}

func average(xs []int64) float64 {
	var total int64
	for _, v := range xs {
		total += v
	}
	return float64(total) / float64(len(xs))
}

func (t *Torrent) onMetadataReceived() {
	t.gotMetainfo.Set()

	t.ti = t.th.TorrentFile()
	t.pieceLength = int64(t.ti.PieceLength())
	t.MakeFiles()

	// Reset fastResumeFile
	infoHash := t.InfoHash()
	t.fastResumeFile = filepath.Join(t.Service.config.TorrentsPath, fmt.Sprintf("%s.fastresume", infoHash))
	t.partsFile = filepath.Join(t.Service.config.DownloadPath, fmt.Sprintf(".%s.parts", infoHash))

	go t.SaveMetainfo(t.Service.config.TorrentsPath)
}

// HasMetadata ...
func (t *Torrent) HasMetadata() bool {
	status := t.th.Status(uint(lt.TorrentHandleQueryName))
	return status.GetHasMetadata()
}

// GetHandle ...
func (t *Torrent) GetHandle() lt.TorrentHandle {
	return t.th
}

// GetPaused ...
func (t *Torrent) GetPaused() bool {
	ts := t.th.Status()
	return ts.GetPaused()
}
