package bittorrent

import (
	"bufio"
	"bytes"
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
	"time"

	gotorrent "github.com/anacrolix/torrent"
	"github.com/dustin/go-humanize"
	"github.com/op/go-logging"

	"github.com/elgatito/elementum/broadcast"
	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/database"
	"github.com/elgatito/elementum/library"
	estorage "github.com/elgatito/elementum/storage"
	"github.com/elgatito/elementum/tmdb"
	"github.com/elgatito/elementum/trakt"
	"github.com/elgatito/elementum/xbmc"
)

const (
	endBufferSize     int64 = 5400276 // ~5.5mb
	playbackMaxWait         = 30 * time.Second
	minCandidateSize        = 100 * 1024 * 1024
	episodeMatchRegex       = `(?i)(^|\W|_)(S0*?%[1]d\W?E0*?%[2]d|%[1]dx0*?%[2]d)(\W|_)`
)

// BTPlayer ...
type BTPlayer struct {
	s                    *BTService
	p                    *PlayerParams
	log                  *logging.Logger
	dialogProgress       *xbmc.DialogProgress
	overlayStatus        *xbmc.OverlayStatus
	torrentFile          string
	scrobble             bool
	deleteAfter          bool
	keepDownloading      int
	keepFilesPlaying     int
	keepFilesFinished    int
	overlayStatusEnabled bool
	Torrent              *Torrent
	chosenFile           *gotorrent.File
	subtitlesFile        *gotorrent.File
	fileSize             int64
	fileName             string
	torrentName          string
	extracted            string
	hasChosenFile        bool
	isDownloading        bool
	notEnoughSpace       bool
	bufferEvents         *broadcast.Broadcaster
	closing              chan interface{}
	closed               bool
}

// PlayerParams ...
type PlayerParams struct {
	Playing       bool
	Paused        bool
	Seeked        bool
	WasPlaying    bool
	FromLibrary   bool
	KodiPosition  int
	WatchedTime   float64
	VideoDuration float64
	URI           string
	FileIndex     int
	ResumeIndex   int
	ContentType   string
	KodiID        int
	TMDBId        int
	ShowID        int
	Season        int
	Episode       int
}

type candidateFile struct {
	Index    int
	Filename string
}

type byFilename []*candidateFile

func (a byFilename) Len() int           { return len(a) }
func (a byFilename) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a byFilename) Less(i, j int) bool { return a[i].Filename < a[j].Filename }

// NewBTPlayer ...
func NewBTPlayer(bts *BTService, params PlayerParams) *BTPlayer {
	params.Playing = true

	btp := &BTPlayer{
		log: logging.MustGetLogger("btplayer"),
		s:   bts,
		p:   &params,

		overlayStatusEnabled: config.Get().EnableOverlayStatus == true,
		keepDownloading:      config.Get().KeepDownloading,
		keepFilesPlaying:     config.Get().KeepFilesPlaying,
		keepFilesFinished:    config.Get().KeepFilesFinished,
		scrobble:             config.Get().Scrobble == true && params.TMDBId > 0 && config.Get().TraktToken != "",
		torrentFile:          "",
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

func (btp *BTPlayer) addTorrent() error {
	if btp.Torrent == nil {
		torrent, err := btp.s.AddTorrent(btp.p.URI)
		if err != nil {
			return err
		}

		btp.Torrent = torrent
	}

	if btp.Torrent == nil || btp.Torrent.Torrent == nil {
		return fmt.Errorf("Unable to add torrent with URI %s", btp.p.URI)
	}

	return nil
}

// PlayURL ...
func (btp *BTPlayer) PlayURL() string {
	if btp.Torrent.IsRarArchive {
		extractedPath := filepath.Join(filepath.Dir(btp.chosenFile.Path()), "extracted", btp.extracted)
		return strings.Join(strings.Split(extractedPath, string(os.PathSeparator)), "/")
	}
	return strings.Join(strings.Split(btp.chosenFile.Path(), string(os.PathSeparator)), "/")
}

// Buffer ...
func (btp *BTPlayer) Buffer() error {
	if btp.p.ResumeIndex >= 0 {
		btp.Torrent.Resume()
	} else if err := btp.addTorrent(); err != nil {
		return err
	}

	btp.onMetadataReceived()
	go btp.s.AttachPlayer(btp)

	buffered, done := btp.bufferEvents.Listen()
	defer close(done)

	btp.dialogProgress = xbmc.NewDialogProgress("Elementum", "", "", "")
	defer btp.dialogProgress.Close()

	btp.overlayStatus = xbmc.NewOverlayStatus()

	go btp.waitCheckAvailableSpace()
	go btp.playerLoop()

	if err := <-buffered; err != nil {
		return err.(error)
	} else if !btp.HasChosenFile() {
		return errors.New("File not chosen")
	}

	return nil
}

func (btp *BTPlayer) waitCheckAvailableSpace() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if btp.hasChosenFile && btp.isDownloading {
				status := btp.s.CheckAvailableSpace(btp.Torrent)
				if !status {
					btp.bufferEvents.Broadcast(errors.New("Not enough space on download destination"))
					btp.notEnoughSpace = true
				}

				return
			}
		}
	}
}

func (btp *BTPlayer) onMetadataReceived() {
	btp.log.Info("Metadata received.")

	btp.torrentFile = filepath.Join(btp.s.config.TorrentsPath, fmt.Sprintf("%s.torrent", btp.Torrent.InfoHash()))
	btp.torrentName = btp.Torrent.Info().Name

	btp.log.Infof("Downloading %s", btp.torrentName)

	// if btp.resumeIndex < 0 {
	// 	btp.Torrent.AutoManaged(false)
	// 	btp.Torrent.Pause()
	// 	defer btp.Torrent.AutoManaged(true)
	// }

	var err error
	btp.chosenFile, err = btp.chooseFile()
	if err != nil {
		btp.bufferEvents.Broadcast(err)
		return
	}

	infoHash := btp.Torrent.InfoHash()

	btp.hasChosenFile = true
	btp.fileSize = btp.chosenFile.Length()
	btp.fileName = filepath.Base(btp.chosenFile.Path())
	btp.subtitlesFile = btp.findSubtitlesFile()

	btp.log.Infof("Chosen file: %s", btp.fileName)
	btp.log.Infof("Saving torrent to database")

	database.Get().UpdateBTItem(infoHash, btp.p.TMDBId, btp.p.ContentType, []*gotorrent.File{btp.chosenFile, btp.subtitlesFile}, btp.p.ShowID, btp.p.Season, btp.p.Episode)
	btp.Torrent.DBItem = database.Get().GetBTItem(infoHash)

	btp.log.Info("Setting file priorities")
	if btp.chosenFile != nil {
		btp.Torrent.DownloadFile(btp.chosenFile)
	}
	if btp.subtitlesFile != nil {
		btp.Torrent.DownloadFile(btp.subtitlesFile)
	}

	btp.log.Info("Setting piece priorities")

	go btp.Torrent.Buffer(btp.chosenFile)

	// TODO find usage of resumeIndex. Do we need pause/resume for it?
	// if btp.resumeIndex < 0 {
	// 	btp.Torrent.Pause()
	// }
}

func (btp *BTPlayer) statusStrings(progress float64) (string, string, string) {
	line1 := fmt.Sprintf("%s (%.2f%%)", btp.Torrent.GetStateString(), progress)
	var totalSize int64
	if btp.fileSize > 0 && !btp.Torrent.IsRarArchive {
		totalSize = btp.fileSize
	} else {
		totalSize = btp.Torrent.BytesCompleted() + btp.Torrent.BytesMissing()
	}
	line1 += " - " + humanize.Bytes(uint64(totalSize))

	line2 := fmt.Sprintf("D:%.0fkB/s U:%.0fkB/s S:%d/%d",
		float64(btp.Torrent.DownloadRate)/1024,
		float64(btp.Torrent.UploadRate)/1024,
		btp.Torrent.Stats().ActivePeers,
		btp.Torrent.Stats().TotalPeers,
	)
	line3 := btp.torrentName
	if btp.fileName != "" && !btp.Torrent.IsRarArchive {
		line3 = btp.fileName
	}
	return line1, line2, line3
}

// HasChosenFile ...
func (btp *BTPlayer) HasChosenFile() bool {
	return btp.hasChosenFile && btp.chosenFile != nil
}

func (btp *BTPlayer) chooseFile() (*gotorrent.File, error) {
	biggestFile := 0
	maxSize := int64(0)
	files := btp.Torrent.Files()
	var candidateFiles []int

	for i, f := range files {
		size := f.Length()
		if size > maxSize {
			maxSize = size
			biggestFile = i
		}
		if size > minCandidateSize {
			candidateFiles = append(candidateFiles, i)
		}

		fileName := filepath.Base(f.Path())
		re := regexp.MustCompile("(?i).*\\.rar")
		if re.MatchString(fileName) && size > 10*1024*1024 {
			btp.Torrent.IsRarArchive = true
			if !xbmc.DialogConfirm("Elementum", "LOCALIZE[30303]") {
				btp.notEnoughSpace = true
				return f, errors.New("RAR archive detected and download was cancelled")
			}
			return f, nil
		}
	}

	if len(candidateFiles) > 1 {
		btp.log.Info(fmt.Sprintf("There are %d candidate files", len(candidateFiles)))
		if btp.p.FileIndex >= 0 && btp.p.FileIndex < len(candidateFiles) {
			return files[candidateFiles[btp.p.FileIndex]], nil
		}

		choices := make(byFilename, 0, len(candidateFiles))
		for _, index := range candidateFiles {
			fileName := filepath.Base(files[index].Path())
			candidate := &candidateFile{
				Index:    index,
				Filename: fileName,
			}
			choices = append(choices, candidate)
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

		sort.Sort(byFilename(choices))

		items := make([]string, 0, len(choices))
		for _, choice := range choices {
			items = append(items, choice.Filename)
		}

		choice := xbmc.ListDialog("LOCALIZE[30223]", items...)
		if choice >= 0 {
			return files[choices[choice].Index], nil
		}
		return nil, fmt.Errorf("User cancelled")
	}

	return files[biggestFile], nil
}

func (btp *BTPlayer) findSubtitlesFile() *gotorrent.File {
	extension := filepath.Ext(btp.fileName)
	chosenName := btp.fileName[0 : len(btp.fileName)-len(extension)]
	srtFileName := chosenName + ".srt"

	files := btp.Torrent.Files()

	var lastMatched *gotorrent.File
	countMatched := 0

	for _, file := range files {
		fileName := file.Path()
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
func (btp *BTPlayer) Close() {
	// Prevent double-closing
	if btp.closed {
		return
	}

	btp.closed = true
	close(btp.closing)

	// Torrent was not initialized so just close and return
	if btp.Torrent == nil {
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

	if keepDownloading == false || deleteAnswer == true || btp.notEnoughSpace || btp.s.config.DownloadStorage == estorage.StorageMemory {
		// Delete torrent file
		if len(btp.torrentFile) > 0 {
			if _, err := os.Stat(btp.torrentFile); err == nil {
				btp.log.Infof("Deleting torrent file at %s", btp.torrentFile)
				defer os.Remove(btp.torrentFile)
			}
		}

		infoHash := btp.Torrent.InfoHash()
		savedFilePath := filepath.Join(btp.s.config.TorrentsPath, fmt.Sprintf("%s.torrent", infoHash))
		if _, err := os.Stat(savedFilePath); err == nil {
			btp.log.Infof("Deleting saved torrent file at %s", savedFilePath)
			defer os.Remove(savedFilePath)
		}

		btp.log.Infof("Removed %s from database", btp.Torrent.Name())

		if btp.deleteAfter || deleteAnswer == true || btp.notEnoughSpace {
			btp.log.Info("Removing the torrent and deleting files...")
			btp.s.RemoveTorrent(btp.Torrent, true)
		} else {
			btp.log.Info("Removing the torrent without deleting files...")
			btp.s.RemoveTorrent(btp.Torrent, false)
		}
	}
}

func (btp *BTPlayer) bufferDialog() {
	halfSecond := time.NewTicker(500 * time.Millisecond)
	defer halfSecond.Stop()
	oneSecond := time.NewTicker(1 * time.Second)
	defer oneSecond.Stop()

	for {
		select {
		case <-halfSecond.C:
			if btp.dialogProgress.IsCanceled() || btp.notEnoughSpace {
				errMsg := "User cancelled the buffering"
				btp.log.Info(errMsg)
				btp.bufferEvents.Broadcast(errors.New(errMsg))
				return
			}
		case <-oneSecond.C:
			status := btp.Torrent.GetState()

			// Handle "Checking" state for resumed downloads
			if status == StatusChecking || btp.Torrent.IsRarArchive {
				progress := btp.Torrent.GetBufferProgress()
				line1, line2, line3 := btp.statusStrings(progress)
				btp.dialogProgress.Update(int(progress), line1, line2, line3)

				if btp.Torrent.IsRarArchive && progress >= 100 {
					archivePath := filepath.Join(btp.s.config.DownloadPath, btp.chosenFile.Path())
					destPath := filepath.Join(btp.s.config.DownloadPath, filepath.Dir(btp.chosenFile.Path()), "extracted")

					if _, err := os.Stat(destPath); err == nil {
						btp.findExtracted(destPath)
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
						btp.log.Error(err)
						btp.bufferEvents.Broadcast(err)
						xbmc.Notify("Elementum", "LOCALIZE[30304]", config.AddonIcon())
						return
					}

					scanner := bufio.NewScanner(cmdReader)
					go func() {
						for scanner.Scan() {
							btp.log.Infof("unrar | %s", scanner.Text())
						}
					}()

					err = cmd.Start()
					if err != nil {
						btp.log.Error(err)
						btp.bufferEvents.Broadcast(err)
						xbmc.Notify("Elementum", "LOCALIZE[30305]", config.AddonIcon())
						return
					}

					err = cmd.Wait()
					if err != nil {
						btp.log.Error(err)
						btp.bufferEvents.Broadcast(err)
						xbmc.Notify("Elementum", "LOCALIZE[30306]", config.AddonIcon())
						return
					}

					btp.findExtracted(destPath)
					btp.bufferEvents.Signal()
					return
				}
			} else {
				line1, line2, line3 := btp.statusStrings(btp.Torrent.BufferProgress)
				btp.dialogProgress.Update(int(btp.Torrent.BufferProgress), line1, line2, line3)
				if !btp.Torrent.IsBuffering && btp.Torrent.GetState() != StatusChecking {
					btp.bufferEvents.Signal()
					return
				}
			}
		}
	}
}

func (btp *BTPlayer) findExtracted(destPath string) {
	files, err := ioutil.ReadDir(destPath)
	if err != nil {
		btp.log.Error(err)
		btp.bufferEvents.Broadcast(err)
		xbmc.Notify("Elementum", "LOCALIZE[30307]", config.AddonIcon())
		return
	}
	if len(files) == 1 {
		btp.log.Info("Extracted", files[0].Name())
		btp.extracted = files[0].Name()
	} else {
		for _, file := range files {
			fileName := file.Name()
			re := regexp.MustCompile("(?i).*\\.(mkv|mp4|mov|avi)")
			if re.MatchString(fileName) {
				btp.log.Info("Extracted", fileName)
				btp.extracted = fileName
				break
			}
		}
	}
}

func (btp *BTPlayer) updateWatchTimes() {
	ret := xbmc.GetWatchTimes()
	if ret["error"] != "" {
		return
	}
	btp.p.WatchedTime, _ = strconv.ParseFloat(ret["watchedTime"], 64)
	btp.p.VideoDuration, _ = strconv.ParseFloat(ret["videoDuration"], 64)
}

func (btp *BTPlayer) playerLoop() {
	defer btp.Close()

	btp.log.Info("Buffer loop")

	buffered, bufferDone := btp.bufferEvents.Listen()
	defer close(bufferDone)

	go btp.bufferDialog()

	if err := <-buffered; err != nil {
		return
	}

	btp.log.Info("Waiting for playback...")
	oneSecond := time.NewTicker(1 * time.Second)
	defer oneSecond.Stop()
	playbackTimeout := time.After(playbackMaxWait)

playbackWaitLoop:
	for {
		if xbmc.PlayerIsPlaying() {
			break playbackWaitLoop
		}
		select {
		case <-playbackTimeout:
			btp.log.Warningf("Playback was unable to start after %d seconds. Aborting...", playbackMaxWait/time.Second)
			btp.bufferEvents.Broadcast(errors.New("Playback was unable to start before timeout"))
			return
		case <-oneSecond.C:
		}
	}

	btp.log.Info("Playback loop")
	overlayStatusActive := false
	playing := true

	btp.updateWatchTimes()
	btp.GetIdent()

	btp.log.Infof("Got playback: %fs / %fs", btp.p.WatchedTime, btp.p.VideoDuration)
	if btp.scrobble {
		trakt.Scrobble("start", btp.p.ContentType, btp.p.TMDBId, btp.p.WatchedTime, btp.p.VideoDuration)
	}

	btp.Torrent.IsPlaying = true

playbackLoop:
	for {
		if xbmc.PlayerIsPlaying() == false {
			btp.Torrent.IsPlaying = false
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
					progress := btp.Torrent.GetProgress()
					line1, line2, line3 := btp.statusStrings(progress)
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

	btp.log.Info("Stopped playback")
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
		btp.p.FromLibrary = false
		btp.p.WatchedTime = 0
		btp.p.VideoDuration = 0
	}()

	btp.overlayStatus.Close()
}

// Params returns Params for external use
func (btp *BTPlayer) Params() *PlayerParams {
	return btp.p
}

// UpdateWatched is updating watched progress is Kodi
func (btp *BTPlayer) UpdateWatched() {
	btp.log.Debugf("Updating Watched state: %#v", btp.p)

	if btp.p.VideoDuration == 0 || btp.p.WatchedTime == 0 {
		return
	}

	progress := btp.p.WatchedTime / btp.p.VideoDuration * 100

	log.Infof("Currently at %f%%, KodiID: %d", progress, btp.p.KodiID)

	if progress > 90 {
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
		if btp.p.ContentType == movieType {
			xbmc.SetMovieWatched(btp.p.KodiID, 0, int(btp.p.WatchedTime), int(btp.p.VideoDuration))
		} else if btp.p.ContentType == episodeType {
			xbmc.SetEpisodeWatched(btp.p.KodiID, 0, int(btp.p.WatchedTime), int(btp.p.VideoDuration))
		}
	}
	time.Sleep(200 * time.Millisecond)
	xbmc.Refresh()
}

// IsWatched ...
func (btp *BTPlayer) IsWatched() bool {
	return (100 * btp.p.WatchedTime / btp.p.VideoDuration) > 90
}

func (btp *BTPlayer) smartMatch(choices byFilename) {
	if !config.Get().SmartEpisodeMatch {
		return
	}

	var buf bytes.Buffer
	btp.Torrent.Torrent.Metainfo().Write(&buf)
	b := buf.Bytes()

	show := tmdb.GetShow(btp.p.ShowID, config.Get().Language)
	if show == nil {
		return
	}

	for _, season := range show.Seasons {
		if season.EpisodeCount == 0 {
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
					database.Get().AddTorrentHistory(strconv.Itoa(episode.ID), btp.Torrent.InfoHash(), b)
				}
			}
		}
	}
}

// GetIdent tries to find playing item in Kodi library
func (btp *BTPlayer) GetIdent() {
	if btp.p.TMDBId == 0 || btp.p.KodiID != 0 {
		return
	}

	if btp.p.ContentType == movieType {
		movie, _ := library.GetMovieByTMDB(btp.p.TMDBId)
		if movie != nil {
			btp.p.KodiID = movie.UIDs.Kodi
		}
	} else if btp.p.ContentType == episodeType {
		show, _ := library.GetShowByTMDB(btp.p.ShowID)
		if show != nil {
			episode := show.GetEpisode(btp.p.Season, btp.p.Episode)
			if episode != nil {
				btp.p.KodiID = episode.UIDs.Kodi
			}
		}
	}

	if btp.p.KodiID == 0 {
		log.Debugf("Can't find %s for these parameters: %+v", btp.p.ContentType, btp.p)
	}
}
