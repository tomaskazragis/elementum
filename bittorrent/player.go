package bittorrent

import (
	"bufio"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	lt "github.com/ElementumOrg/libtorrent-go"
	"github.com/dustin/go-humanize"
	"github.com/sanity-io/litter"

	"github.com/elgatito/elementum/broadcast"
	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/database"
	"github.com/elgatito/elementum/diskusage"
	"github.com/elgatito/elementum/library"
	"github.com/elgatito/elementum/tmdb"
	"github.com/elgatito/elementum/trakt"
	"github.com/elgatito/elementum/util"
	"github.com/elgatito/elementum/xbmc"
)

const (
	// EndBufferSize ...
	EndBufferSize     int64 = 5400276 // ~5.5mb
	episodeMatchRegex       = `(?i)(^|\W|_)(S0*?%[1]d\W?E0*?%[2]d|%[1]dx0*?%[2]d)(\W|_)`
)

// Player ...
type Player struct {
	s                        *Service
	t                        *Torrent
	p                        *PlayerParams
	dialogProgress           *xbmc.DialogProgress
	overlayStatus            *xbmc.OverlayStatus
	contentType              string
	scrobble                 bool
	deleteAfter              bool
	keepDownloading          int
	keepFilesPlaying         int
	keepFilesFinished        int
	overlayStatusEnabled     bool
	chosenFile               *File
	subtitlesFile            *File
	fileSize                 int64
	fileName                 string
	extracted                string
	hasChosenFile            bool
	isDownloading            bool
	notEnoughSpace           bool
	bufferEvents             *broadcast.Broadcaster
	bufferPiecesProgress     map[int]float64
	bufferPiecesProgressLock sync.RWMutex

	diskStatus *diskusage.DiskStatus
	closing    chan interface{}
	closed     bool
}

// PlayerParams ...
type PlayerParams struct {
	Playing        bool
	Paused         bool
	Seeked         bool
	WasPlaying     bool
	KodiPosition   int
	WatchedTime    float64
	VideoDuration  float64
	URI            string
	FileIndex      int
	ResumeHash     string
	ResumePlayback bool
	ContentType    string
	KodiID         int
	TMDBId         int
	ShowID         int
	Season         int
	Episode        int
	Query          string
	UIDs           *library.UniqueIDs
	Resume         *library.Resume
}

type candidateFile struct {
	Index       int
	Filename    string
	DisplayName string
}

// NewPlayer ...
func NewPlayer(bts *Service, params PlayerParams) *Player {
	params.Playing = true

	btp := &Player{
		s: bts,
		p: &params,

		overlayStatusEnabled: config.Get().EnableOverlayStatus == true,
		keepDownloading:      config.Get().KeepDownloading,
		keepFilesPlaying:     config.Get().KeepFilesPlaying,
		keepFilesFinished:    config.Get().KeepFilesFinished,
		scrobble:             config.Get().Scrobble == true && params.TMDBId > 0 && config.Get().TraktToken != "",
		hasChosenFile:        false,
		fileSize:             0,
		fileName:             "",
		isDownloading:        false,
		notEnoughSpace:       false,
		closing:              make(chan interface{}),
		bufferEvents:         broadcast.NewBroadcaster(),
	}
	return btp
}

// GetTorrent ...
func (btp *Player) GetTorrent() *Torrent {
	return btp.t
}

// SetTorrent ...
func (btp *Player) SetTorrent(t *Torrent) {
	btp.t = t
}

func (btp *Player) addTorrent() error {
	if btp.t == nil {
		torrent, err := btp.s.AddTorrent(btp.p.URI)
		if err != nil {
			return err
		}

		btp.t = torrent
	}
	if btp.t == nil || btp.t.th == nil {
		return fmt.Errorf("Unable to add torrent with URI %s", btp.p.URI)
	}

	go btp.consumeAlerts()

	log.Infof("Downloading %s", btp.t.Name())

	return nil
}

func (btp *Player) resumeTorrent() error {
	if btp.t == nil || btp.t.th == nil {
		return fmt.Errorf("Unable to resume torrent with index %s", btp.p.ResumeHash)
	}

	go btp.consumeAlerts()

	log.Infof("Resuming %s", btp.t.Name())

	btp.t.th.AutoManaged(true)

	return nil
}

// PlayURL ...
func (btp *Player) PlayURL() string {
	if btp.t.IsRarArchive {
		extractedPath := filepath.Join(filepath.Dir(btp.chosenFile.Path), "extracted", btp.extracted)
		return util.EncodeFileURL(extractedPath)
	}
	return util.EncodeFileURL(btp.chosenFile.Path)
}

// Buffer ...
func (btp *Player) Buffer() error {
	if btp.p.ResumeHash != "" {
		if err := btp.resumeTorrent(); err != nil {
			log.Errorf("Error resuming torrent: %#v", err)
			return err
		}
	} else {
		if err := btp.addTorrent(); err != nil {
			log.Errorf("Error adding torrent: %#v", err)
			return err
		}
	}

	btp.processMetadata()

	btp.t.IsBuffering = true

	buffered, done := btp.bufferEvents.Listen()
	defer close(done)

	btp.dialogProgress = xbmc.NewDialogProgress("Elementum", "", "", "")
	defer btp.dialogProgress.Close()

	btp.overlayStatus = xbmc.NewOverlayStatus()

	go btp.waitCheckAvailableSpace()
	go btp.playerLoop()
	go btp.s.AttachPlayer(btp)

	if err := <-buffered; err != nil {
		return err.(error)
	} else if !btp.HasChosenFile() {
		return errors.New("File not chosen")
	}

	return nil
}

func (btp *Player) waitCheckAvailableSpace() {
	if btp.s.IsMemoryStorage() {
		return
	}

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if btp.hasChosenFile {
				if !btp.s.checkAvailableSpace(btp.t) {
					btp.bufferEvents.Broadcast(errors.New("Not enough space on download destination"))
					btp.notEnoughSpace = true
				}

				return
			}
		}
	}
}

func (btp *Player) processMetadata() {
	// TODO: Do we need it?
	// if btp.p.ResumeHash != "" {
	// 	btp.t.th.AutoManaged(false)
	// 	btp.t.Pause()
	// 	defer btp.t.th.AutoManaged(true)
	// }

	var err error
	btp.chosenFile, err = btp.chooseFile()
	if err != nil {
		btp.bufferEvents.Broadcast(err)
		return
	}

	btp.hasChosenFile = true
	btp.fileSize = btp.chosenFile.Size
	btp.fileName = btp.chosenFile.Name
	btp.subtitlesFile = btp.findSubtitlesFile()

	log.Infof("Chosen file: %s", btp.fileName)
	log.Infof("Saving torrent to database")

	files := []string{}
	if btp.chosenFile != nil {
		files = append(files, btp.chosenFile.Path)
	}
	if btp.subtitlesFile != nil {
		files = append(files, btp.subtitlesFile.Path)
	}

	infoHash := btp.t.InfoHash()
	database.Get().UpdateBTItem(infoHash, btp.p.TMDBId, btp.p.ContentType, files, btp.p.Query, btp.p.ShowID, btp.p.Season, btp.p.Episode)
	btp.t.DBItem = database.Get().GetBTItem(infoHash)

	if btp.t.IsRarArchive {
		// Just disable sequential download for RAR archives
		log.Info("Disabling sequential download")
		btp.t.th.SetSequentialDownload(false)
		return
	}

	// Set all file priorities to 0 except chosen file,
	// or all to 0 for Memory storage
	log.Info("Setting file priorities")
	filesPriorities := lt.NewStdVectorInt()
	defer lt.DeleteStdVectorInt(filesPriorities)

	for _, f := range btp.t.files {
		if btp.s.IsMemoryStorage() {
			filesPriorities.Add(0)
		} else if f == btp.chosenFile {
			filesPriorities.Add(4)
		} else if f == btp.subtitlesFile {
			filesPriorities.Add(4)
		} else {
			filesPriorities.Add(0)
		}
	}
	btp.t.th.PrioritizeFiles(filesPriorities)

	log.Info("Setting piece priorities")

	go btp.t.Buffer(btp.chosenFile)

	// TODO find usage of resumeIndex. Do we need pause/resume for it?
	// if btp.resumeIndex < 0 {
	// 	btp.Torrent.Pause()
	// }
}

func (btp *Player) statusStrings(progress float64, status lt.TorrentStatus) (string, string, string) {
	statusName := StatusStrings[int(status.GetState())]
	if btp.t.IsBuffering {
		statusName = StatusStrings[StatusBuffering]
	} else if btp.s.IsMemoryStorage() {
		statusName = StatusStrings[StatusDownloading]
	}

	line1 := fmt.Sprintf("%s (%.2f%%)", statusName, progress)
	if btp.t.ti != nil && btp.t.ti.Swigcptr() != 0 {
		var totalSize int64
		if btp.fileSize > 0 && !btp.t.IsRarArchive {
			totalSize = btp.fileSize
		} else {
			totalSize = btp.t.ti.TotalSize()
		}
		line1 += " - " + humanize.Bytes(uint64(totalSize))
	}

	seeders := status.GetNumSeeds()
	line2 := fmt.Sprintf("D:%.0fkB/s U:%.0fkB/s S:%d/%d P:%d/%d",
		float64(status.GetDownloadPayloadRate())/1024,
		float64(status.GetUploadPayloadRate())/1024,
		seeders,
		status.GetNumComplete(),
		status.GetNumPeers()-seeders,
		status.GetNumIncomplete(),
	)
	line3 := btp.t.Name()
	if btp.fileName != "" && !btp.t.IsRarArchive {
		line3 = btp.fileName
	}
	return line1, line2, line3
}

// HasChosenFile ...
func (btp *Player) HasChosenFile() bool {
	return btp.hasChosenFile && btp.chosenFile != nil
}

func (btp *Player) chooseFile() (*File, error) {
	biggestFile := 0
	maxSize := int64(0)
	files := btp.t.files
	isBluRay := false
	var candidateFiles []int

	for i, f := range files {
		size := f.Size
		if size > maxSize {
			maxSize = size
			biggestFile = i
		}
		if size > config.Get().MinCandidateSize {
			candidateFiles = append(candidateFiles, i)
		}
		if strings.Contains(f.Path, "BDMV/STREAM/") {
			isBluRay = true
			continue
		}

		fileName := filepath.Base(f.Path)
		re := regexp.MustCompile("(?i).*\\.rar")
		if re.MatchString(fileName) && size > 10*1024*1024 {
			btp.t.IsRarArchive = true
			if !xbmc.DialogConfirm("Elementum", "LOCALIZE[30303]") {
				btp.notEnoughSpace = true
				return f, errors.New("RAR archive detected and download was cancelled")
			}
			return f, nil
		}
	}
	if isBluRay {
		log.Info("Skipping file choose, as this is a BluRay stream.")
		return files[biggestFile], nil
	}

	if len(candidateFiles) > 1 {
		log.Info(fmt.Sprintf("There are %d candidate files", len(candidateFiles)))
		if btp.p.FileIndex >= 0 && btp.p.FileIndex < len(candidateFiles) {
			return files[candidateFiles[btp.p.FileIndex]], nil
		}

		choices := make([]*candidateFile, 0, len(candidateFiles))
		for _, index := range candidateFiles {
			fileName := filepath.Base(files[index].Path)
			candidate := &candidateFile{
				Index:       index,
				Filename:    fileName,
				DisplayName: files[index].Path,
			}
			choices = append(choices, candidate)
		}

		// We are trying to see whether all files belong to the same directory.
		// If yes - we can remove that directory from printed files list
		for _, d := range strings.Split(choices[0].DisplayName, "/") {
			ret := true
			for _, c := range choices {
				if !strings.HasPrefix(c.DisplayName, d+"/") {
					ret = false
					break
				}
			}

			if ret {
				for _, c := range choices {
					c.DisplayName = strings.Replace(c.DisplayName, d+"/", "", 1)
				}
			} else {
				break
			}
		}

		// Adding sizes to file names
		for _, c := range choices {
			if btp.p.Episode == 0 {
				c.DisplayName += " [" + humanize.Bytes(uint64(files[c.Index].Size)) + "]"
			}
		}

		if btp.p.Episode > 0 {
			// In episode search we are using smart-match to store found episodes
			//   in the torrent history table
			go btp.smartMatch(choices)

			var lastMatched int
			var foundMatches int
			// Case-insensitive, starting with a line-start or non-ascii, can have leading zeros, followed by non-ascii
			// TODO: Add logic for matching S01E0102 (double episode filename)
			re := regexp.MustCompile(fmt.Sprintf(episodeMatchRegex, btp.p.Season, btp.p.Episode))
			for index, choice := range choices {
				if re.MatchString(choice.Filename) {
					lastMatched = index
					foundMatches++
				}
			}

			if foundMatches == 1 {
				return files[choices[lastMatched].Index], nil
			}
		}

		sort.Slice(choices, func(i, j int) bool {
			return choices[i].DisplayName < choices[j].DisplayName
		})

		items := make([]string, 0, len(choices))
		for _, choice := range choices {
			items = append(items, choice.DisplayName)
		}

		choice := xbmc.ListDialog("LOCALIZE[30223]", items...)
		if choice >= 0 {
			return files[choices[choice].Index], nil
		}
		return nil, fmt.Errorf("User cancelled")
	}

	return files[biggestFile], nil
}

func (btp *Player) findSubtitlesFile() *File {
	extension := filepath.Ext(btp.fileName)
	chosenName := btp.fileName[0 : len(btp.fileName)-len(extension)]
	srtFileName := chosenName + ".srt"

	files := btp.t.files

	var lastMatched *File
	countMatched := 0

	for _, file := range files {
		fileName := file.Path
		if strings.HasSuffix(fileName, srtFileName) {
			return file
		} else if strings.HasSuffix(fileName, ".srt") {
			lastMatched = file
			countMatched++
		}
	}

	if countMatched == 1 {
		return lastMatched
	}

	return nil
}

// Close ...
func (btp *Player) Close() {
	// Prevent double-closing
	if btp.closed {
		return
	}

	btp.closed = true
	close(btp.closing)

	// Torrent was not initialized so just close and return
	if btp.t == nil {
		return
	}

	defer func() {
		go btp.s.DetachPlayer(btp)
		go btp.s.PlayerStop()
	}()

	isWatched := btp.IsWatched()
	keepDownloading := false
	if btp.keepDownloading == 2 {
		keepDownloading = false
	} else if btp.keepDownloading == 0 || xbmc.DialogConfirm("Elementum", "LOCALIZE[30146]") {
		keepDownloading = true
	}

	keepSetting := btp.keepFilesPlaying
	if isWatched {
		keepSetting = btp.keepFilesFinished
	}

	deleteAnswer := false
	if keepDownloading == false {
		if keepSetting == 0 {
			deleteAnswer = false
		} else if keepSetting == 2 || xbmc.DialogConfirm("Elementum", "LOCALIZE[30269]") {
			deleteAnswer = true
		}
	}

	if keepDownloading == false || deleteAnswer == true || btp.notEnoughSpace || btp.s.IsMemoryStorage() {
		// Delete torrent file
		if len(btp.t.torrentFile) > 0 {
			if _, err := os.Stat(btp.t.torrentFile); err == nil {
				log.Infof("Deleting torrent file at %s", btp.t.torrentFile)
				defer os.Remove(btp.t.torrentFile)
			}
		}

		infoHash := btp.t.InfoHash()
		savedFilePath := filepath.Join(btp.s.config.TorrentsPath, fmt.Sprintf("%s.torrent", infoHash))
		if _, err := os.Stat(savedFilePath); err == nil {
			log.Infof("Deleting saved torrent file at %s", savedFilePath)
			defer os.Remove(savedFilePath)
		}

		log.Infof("Removed %s from database", btp.t.Name())

		if btp.deleteAfter || deleteAnswer == true || btp.notEnoughSpace {
			log.Info("Removing the torrent and deleting files after playing ...")
			btp.s.RemoveTorrent(btp.t, true)
		} else {
			log.Info("Removing the torrent without deleting files after playing ...")
			btp.s.RemoveTorrent(btp.t, false)
		}
	}
}

func (btp *Player) bufferDialog() {
	halfSecond := time.NewTicker(500 * time.Millisecond)
	defer halfSecond.Stop()
	oneSecond := time.NewTicker(1 * time.Second)
	defer oneSecond.Stop()

	for {
		select {
		case <-halfSecond.C:
			if btp.dialogProgress.IsCanceled() || btp.notEnoughSpace {
				errMsg := "User cancelled the buffering"
				log.Info(errMsg)
				btp.bufferEvents.Broadcast(errors.New(errMsg))
				return
			}
		case <-oneSecond.C:
			status := btp.t.GetStatus()

			// Handle "Checking" state for resumed downloads
			if status.GetState() == StatusChecking || btp.t.IsRarArchive {
				progress := btp.t.GetBufferProgress()
				line1, line2, line3 := btp.statusStrings(progress, status)
				btp.dialogProgress.Update(int(progress), line1, line2, line3)

				if btp.t.IsRarArchive && progress >= 100 {
					archivePath := filepath.Join(btp.s.config.DownloadPath, btp.chosenFile.Path)
					destPath := filepath.Join(btp.s.config.DownloadPath, filepath.Dir(btp.chosenFile.Path), "extracted")

					if _, err := os.Stat(destPath); err == nil {
						btp.findExtracted(destPath)
						btp.setRateLimiting(true)
						btp.bufferEvents.Signal()
						return
					}
					os.MkdirAll(destPath, 0755)

					cmdName := "unrar"
					cmdArgs := []string{"e", archivePath, destPath}
					if platform := xbmc.GetPlatform(); platform.OS == "windows" {
						cmdName = "unrar.exe"
					}
					cmd := exec.Command(cmdName, cmdArgs...)

					cmdReader, err := cmd.StdoutPipe()
					if err != nil {
						log.Error(err)
						btp.bufferEvents.Broadcast(err)
						xbmc.Notify("Elementum", "LOCALIZE[30304]", config.AddonIcon())
						return
					}

					scanner := bufio.NewScanner(cmdReader)
					go func() {
						for scanner.Scan() {
							log.Infof("unrar | %s", scanner.Text())
						}
					}()

					err = cmd.Start()
					if err != nil {
						log.Error(err)
						btp.bufferEvents.Broadcast(err)
						xbmc.Notify("Elementum", "LOCALIZE[30305]", config.AddonIcon())
						return
					}

					err = cmd.Wait()
					if err != nil {
						log.Error(err)
						btp.bufferEvents.Broadcast(err)
						xbmc.Notify("Elementum", "LOCALIZE[30306]", config.AddonIcon())
						return
					}

					btp.findExtracted(destPath)
					btp.setRateLimiting(true)
					btp.bufferEvents.Signal()
					return
				}
			} else {
				status := btp.t.GetStatus()
				line1, line2, line3 := btp.statusStrings(btp.t.BufferProgress, status)
				btp.dialogProgress.Update(int(btp.t.BufferProgress), line1, line2, line3)
				if !btp.t.IsBuffering && btp.t.HasMetadata() && btp.t.GetState() != StatusChecking {
					btp.bufferEvents.Signal()
					btp.setRateLimiting(true)
					return
				}
			}
		}
	}
}

func (btp *Player) findExtracted(destPath string) {
	files, err := ioutil.ReadDir(destPath)
	if err != nil {
		log.Error(err)
		btp.bufferEvents.Broadcast(err)
		xbmc.Notify("Elementum", "LOCALIZE[30307]", config.AddonIcon())
		return
	}
	if len(files) == 1 {
		log.Info("Extracted", files[0].Name())
		btp.extracted = files[0].Name()
	} else {
		for _, file := range files {
			fileName := file.Name()
			re := regexp.MustCompile("(?i).*\\.(mkv|mp4|mov|avi)")
			if re.MatchString(fileName) {
				log.Info("Extracted", fileName)
				btp.extracted = fileName
				break
			}
		}
	}
}

func (btp *Player) updateWatchTimes() {
	ret := xbmc.GetWatchTimes()
	if ret["error"] != "" {
		return
	}
	btp.p.WatchedTime, _ = strconv.ParseFloat(ret["watchedTime"], 64)
	btp.p.VideoDuration, _ = strconv.ParseFloat(ret["videoDuration"], 64)
}

func (btp *Player) playerLoop() {
	defer btp.Close()

	log.Info("Buffer loop")

	buffered, bufferDone := btp.bufferEvents.Listen()
	defer close(bufferDone)

	go btp.bufferDialog()

	if err := <-buffered; err != nil {
		log.Errorf("Error buffering: %#v", err)
		return
	}

	log.Info("Waiting for playback...")
	oneSecond := time.NewTicker(1 * time.Second)
	defer oneSecond.Stop()
	playbackTimeout := time.After(time.Duration(config.Get().BufferTimeout) * time.Second)

playbackWaitLoop:
	for {
		if xbmc.PlayerIsPlaying() {
			break playbackWaitLoop
		}
		select {
		case <-playbackTimeout:
			log.Warningf("Playback was unable to start after %d seconds. Aborting...", config.Get().BufferTimeout)
			btp.bufferEvents.Broadcast(errors.New("Playback was unable to start before timeout"))
			return
		case <-oneSecond.C:
		}
	}

	log.Info("Playback loop")
	overlayStatusActive := false
	playing := true

	btp.updateWatchTimes()
	btp.GetIdent()

	log.Infof("Got playback: %fs / %fs", btp.p.WatchedTime, btp.p.VideoDuration)
	if btp.scrobble {
		trakt.Scrobble("start", btp.p.ContentType, btp.p.TMDBId, btp.p.WatchedTime, btp.p.VideoDuration)
	}

	btp.t.IsPlaying = true

playbackLoop:
	for {
		if xbmc.PlayerIsPlaying() == false {
			btp.t.IsPlaying = false
			break playbackLoop
		}
		select {
		case <-oneSecond.C:
			if btp.p.Seeked {
				btp.p.Seeked = false
				btp.updateWatchTimes()
				if btp.scrobble {
					trakt.Scrobble("start", btp.p.ContentType, btp.p.TMDBId, btp.p.WatchedTime, btp.p.VideoDuration)
				}
			} else if xbmc.PlayerIsPaused() {
				if playing == true {
					playing = false
					btp.updateWatchTimes()
					if btp.scrobble {
						trakt.Scrobble("pause", btp.p.ContentType, btp.p.TMDBId, btp.p.WatchedTime, btp.p.VideoDuration)
					}
				}
				if btp.overlayStatusEnabled == true {
					status := btp.t.GetStatus()
					progress := btp.t.GetProgress()
					line1, line2, line3 := btp.statusStrings(progress, status)
					btp.overlayStatus.Update(int(progress), line1, line2, line3)
					if overlayStatusActive == false {
						btp.overlayStatus.Show()
						overlayStatusActive = true
					}
				}
			} else {
				btp.updateWatchTimes()
				if playing == false {
					playing = true
					if btp.scrobble {
						trakt.Scrobble("start", btp.p.ContentType, btp.p.TMDBId, btp.p.WatchedTime, btp.p.VideoDuration)
					}
				}
				if overlayStatusActive == true {
					btp.overlayStatus.Hide()
					overlayStatusActive = false
				}
			}
		}
	}

	log.Info("Stopped playback")
	btp.setRateLimiting(false)
	go func() {
		btp.GetIdent()
		btp.UpdateWatched()
		if btp.scrobble {
			trakt.Scrobble("stop", btp.p.ContentType, btp.p.TMDBId, btp.p.WatchedTime, btp.p.VideoDuration)
		}

		btp.p.Playing = false
		btp.p.Paused = false
		btp.p.Seeked = false
		btp.p.WasPlaying = true
		btp.p.WatchedTime = 0
		btp.p.VideoDuration = 0
	}()

	btp.overlayStatus.Close()
}

// Params returns Params for external use
func (btp *Player) Params() *PlayerParams {
	return btp.p
}

// UpdateWatched is updating watched progress is Kodi
func (btp *Player) UpdateWatched() {
	log.Debugf("Updating Watched state: %s", litter.Sdump(btp.p))

	if btp.p.VideoDuration == 0 || btp.p.WatchedTime == 0 {
		return
	}

	progress := btp.p.WatchedTime / btp.p.VideoDuration * 100

	log.Infof("Currently at %f%%, KodiID: %d", progress, btp.p.KodiID)

	if progress > float64(config.Get().PlaybackPercent) {
		var watched *trakt.WatchedItem

		// TODO: Make use of Playcount, possibly increment when Watched, use old value if in progress
		if btp.p.ContentType == movieType {
			watched = &trakt.WatchedItem{
				MediaType: btp.p.ContentType,
				Movie:     btp.p.TMDBId,
				Watched:   true,
			}
			if btp.p.KodiID != 0 {
				xbmc.SetMovieWatched(btp.p.KodiID, 1, 0, 0)
			}
		} else if btp.p.ContentType == episodeType {
			watched = &trakt.WatchedItem{
				MediaType: btp.p.ContentType,
				Show:      btp.p.ShowID,
				Season:    btp.p.Season,
				Episode:   btp.p.Episode,
				Watched:   true,
			}
			if btp.p.KodiID != 0 {
				xbmc.SetEpisodeWatched(btp.p.KodiID, 1, 0, 0)
			}
		}

		// We set Trakt watched only if it's not in Kodi library
		// to track items that are started from Elementum lists
		// otherwise we will get Watched items set twice in Trakt
		if config.Get().TraktToken != "" && watched != nil && btp.p.KodiID == 0 {
			log.Debugf("Setting Trakt watched for: %#v", watched)
			go trakt.SetWatched(watched)
		}
	} else if btp.p.WatchedTime > 180 {
		if btp.p.Resume != nil {
			log.Debugf("Updating player resume from: %#v", btp.p.Resume)
			btp.p.Resume.Position = btp.p.WatchedTime
			btp.p.Resume.Total = btp.p.VideoDuration
		}

		if btp.p.ContentType == movieType {
			xbmc.SetMovieProgress(btp.p.KodiID, int(btp.p.WatchedTime), int(btp.p.VideoDuration))
		} else if btp.p.ContentType == episodeType {
			xbmc.SetEpisodeProgress(btp.p.KodiID, int(btp.p.WatchedTime), int(btp.p.VideoDuration))
		}
	}
	time.Sleep(200 * time.Millisecond)
	xbmc.Refresh()
}

// IsWatched ...
func (btp *Player) IsWatched() bool {
	return (100 * btp.p.WatchedTime / btp.p.VideoDuration) > float64(config.Get().PlaybackPercent)
}

func (btp *Player) smartMatch(choices []*candidateFile) {
	if !config.Get().SmartEpisodeMatch {
		return
	}

	b := btp.t.GetMetadata()
	show := tmdb.GetShow(btp.p.ShowID, config.Get().Language)
	if show == nil {
		return
	}

	for _, season := range show.Seasons {
		if season == nil || season.EpisodeCount == 0 {
			continue
		}
		episodes := tmdb.GetSeason(btp.p.ShowID, season.Season, config.Get().Language).Episodes

		for _, episode := range episodes {
			if episode == nil {
				continue
			}

			re := regexp.MustCompile(fmt.Sprintf(episodeMatchRegex, season.Season, episode.EpisodeNumber))
			for _, choice := range choices {
				if re.MatchString(choice.Filename) {
					database.Get().AddTorrentHistory(strconv.Itoa(episode.ID), btp.t.InfoHash(), b)
				}
			}
		}
	}
}

// GetIdent tries to find playing item in Kodi library
func (btp *Player) GetIdent() {
	if btp.p.TMDBId == 0 || btp.p.KodiID != 0 {
		return
	}

	if btp.p.ContentType == movieType {
		movie, _ := library.GetMovieByTMDB(btp.p.TMDBId)
		if movie != nil {
			btp.p.KodiID = movie.UIDs.Kodi
			btp.p.Resume = movie.Resume
			btp.p.UIDs = movie.UIDs
		}
	} else if btp.p.ContentType == episodeType {
		show, _ := library.GetShowByTMDB(btp.p.ShowID)
		if show != nil {
			episode := show.GetEpisode(btp.p.Season, btp.p.Episode)
			if episode != nil {
				btp.p.KodiID = episode.UIDs.Kodi
				btp.p.Resume = episode.Resume
				btp.p.UIDs = episode.UIDs
			}
		}
	}

	if btp.p.KodiID == 0 {
		log.Debugf("Can't find %s for these parameters: %+v", btp.p.ContentType, btp.p)
	}
}

func (btp *Player) setRateLimiting(enable bool) {
	if btp.s.config.LimitAfterBuffering {
		settings := btp.s.PackSettings
		if enable == true {
			if btp.s.config.DownloadRateLimit > 0 {
				log.Infof("Buffer filled, rate limiting download to %s", humanize.Bytes(uint64(btp.s.config.DownloadRateLimit)))
				settings.SetInt("download_rate_limit", btp.s.config.UploadRateLimit)
			}
			if btp.s.config.UploadRateLimit > 0 {
				// If we have an upload rate, use the nicer bittyrant choker
				log.Infof("Buffer filled, rate limiting upload to %s", humanize.Bytes(uint64(btp.s.config.UploadRateLimit)))
				settings.SetInt("upload_rate_limit", btp.s.config.UploadRateLimit)
			}
		} else {
			log.Info("Resetting rate limiting")
			settings.SetInt("download_rate_limit", 0)
			settings.SetInt("upload_rate_limit", 0)
		}
		btp.s.Session.GetHandle().ApplySettings(settings)
	}
}

func (btp *Player) consumeAlerts() {
	log.Debugf("Consuming alerts")
	alerts, alertsDone := btp.s.Alerts()
	defer close(alertsDone)

	for {
		select {
		case alert, ok := <-alerts:
			if !ok { // was the alerts channel closed?
				return
			}

			switch alert.Type {
			case lt.StateChangedAlertAlertType:
				stateAlert := lt.SwigcptrStateChangedAlert(alert.Pointer)
				if stateAlert.GetHandle().Equal(btp.t.th) {
					btp.onStateChanged(stateAlert)
				}
			}
		case <-btp.closing:
			log.Debugf("Stopping player alerts")
			return
		}
	}
}

func (btp *Player) onStateChanged(stateAlert lt.StateChangedAlert) {
	switch stateAlert.GetState() {
	case lt.TorrentStatusDownloading:
		btp.isDownloading = true
	}
}
