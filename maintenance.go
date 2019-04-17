package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/elgatito/elementum/bittorrent"
	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/library"
	"github.com/elgatito/elementum/xbmc"
)

const (
	movieType   = "movie"
	showType    = "tvshow"
	seasonType  = "season"
	episodeType = "episode"
)

// Notification serves callbacks from Kodi
func Notification(w http.ResponseWriter, r *http.Request, s *bittorrent.Service) {
	sender := r.URL.Query().Get("sender")
	method := r.URL.Query().Get("method")
	data := r.URL.Query().Get("data")

	jsonData, jsonErr := base64.StdEncoding.DecodeString(data)
	if jsonErr != nil {
		// Base64 is not URL safe and, probably, Kodi is not well encoding it,
		// so we just take it from URL and decode.
		// Hoping "data=" is always in the end of url.
		if strings.Contains(r.URL.RawQuery, "&data=") {
			data = r.URL.RawQuery[strings.Index(r.URL.RawQuery, "&data=")+6:]
		}
		jsonData, _ = base64.StdEncoding.DecodeString(data)
	}
	log.Debugf("Got notification from %s/%s: %s", sender, method, string(jsonData))

	if sender != "xbmc" {
		return
	}

	switch method {
	case "Playlist.OnAdd":
		p := s.GetActivePlayer()
		if p == nil || p.Params().VideoDuration == 0 {
			return
		}
		var request struct {
			Item struct {
				ID   int    `json:"id"`
				Type string `json:"type"`
			} `json:"item"`
			Position int `json:"position"`
		}
		request.Position = -1

		if err := json.Unmarshal(jsonData, &request); err != nil {
			log.Error(err)
			return
		}
		p.Params().KodiPosition = request.Position

	case "Player.OnSeek":
		p := s.GetActivePlayer()
		if p == nil || p.Params().VideoDuration == 0 {
			return
		}
		p.Params().Seeked = true
		// Run prioritization over Player's torrent
		go p.GetTorrent().PrioritizePieces()

	case "Player.OnPause":
		p := s.GetActivePlayer()
		if p == nil || p.Params().VideoDuration == 0 {
			return
		}

		if !p.Params().Paused {
			p.Params().Paused = true
		}

	case "Player.OnPlay":
		time.Sleep(400 * time.Millisecond) // Let player get its WatchedTime and VideoDuration
		p := s.GetActivePlayer()
		if p == nil {
			return
		}

		for i := 0; i <= 6; i++ {
			if p.Params().VideoDuration != 0 {
				break
			}

			time.Sleep(500 * time.Millisecond)
		}

		if p.Params().VideoDuration == 0 {
			log.Warningf("Cannot get player's VideoDuration")
			return
		}

		// Do not need to seek if it's not starting from the beginning of file
		if p.Params().WatchedTime > 30 {
			log.Infof("Skipping seek for already-seeked player")
			return
		}

		if p.Params().Paused { // Prevent seeking when simply unpausing
			p.Params().Paused = false
			log.Infof("Skipping seek for paused player")
			return
		}

		log.Debugf("OnPlay Resume check. KodiPosition: %#v, Resume: %#v, StoredResume: %#v", p.Params().KodiPosition, p.Params().Resume, p.Params().StoredResume)
		if p.Params().StoredResume != nil {
			pos := p.Params().StoredResume.Position
			if config.Get().PlayResumeBack > 0 {
				pos -= float64(config.Get().PlayResumeBack)
				if pos < 0 {
					pos = 0
				}
			}

			xbmc.PlayerSeek(pos)
			return
		}

		if !config.Get().PlayResume || p.Params().KodiPosition == 0 || p.Params().Resume == nil {
			return
		}
		var started struct {
			Item struct {
				ID   int    `json:"id"`
				Type string `json:"type"`
			} `json:"item"`
		}
		if err := json.Unmarshal(jsonData, &started); err != nil {
			log.Error(err)
			return
		}

		if p.Params().Resume.Position > 0 && p.Params().ResumePlayback {
			pos := p.Params().Resume.Position
			if config.Get().PlayResumeBack > 0 {
				pos -= float64(config.Get().PlayResumeBack)
				if pos < 0 {
					pos = 0
				}
			}

			xbmc.PlayerSeek(pos)
		}

	case "Player.OnStop":
		p := s.GetActivePlayer()
		if p == nil || p.Params().VideoDuration <= 1 {
			return
		}

		var stopped struct {
			Ended bool `json:"end"`
			Item  struct {
				ID   int    `json:"id"`
				Type string `json:"type"`
			} `json:"item"`
		}
		if err := json.Unmarshal(jsonData, &stopped); err != nil {
			log.Error(err)
			return
		}

		progress := p.Params().WatchedTime / p.Params().VideoDuration * 100

		log.Infof("Stopped at %f%%", progress)

	case "Playlist.OnClear":
		// TODO: Do we need this endpoint?

	case "VideoLibrary.OnUpdate":
		if library.Scanning {
			return
		}

		time.Sleep(300 * time.Millisecond) // Because Kodi...
		var request struct {
			Item struct {
				ID   int    `json:"id"`
				Type string `json:"type"`
			} `json:"item"`
			Playcount int `json:"playcount"`
		}
		request.Playcount = -1
		if err := json.Unmarshal(jsonData, &request); err != nil {
			log.Error(err)
			return
		}

		if request.Item.Type == movieType {
			library.RefreshMovie(request.Item.ID, library.ActionUpdate)
		} else if request.Item.Type == showType {
			library.RefreshShow(request.Item.ID, library.ActionUpdate)
		} else if request.Item.Type == episodeType {
			library.RefreshEpisode(request.Item.ID, library.ActionUpdate)
		}

	case "VideoLibrary.OnRemove":
		var item struct {
			ID   int    `json:"id"`
			Type string `json:"type"`
		}
		if err := json.Unmarshal(jsonData, &item); err != nil {
			log.Error(err)
			return
		}

		if item.Type == movieType {
			library.RefreshMovie(item.ID, library.ActionSafeDelete)
		} else if item.Type == showType {
			library.RefreshShow(item.ID, library.ActionSafeDelete)
		} else if item.Type == episodeType {
			library.RefreshEpisode(item.ID, library.ActionSafeDelete)
		}

	case "VideoLibrary.OnScanFinished":
		go library.RefreshOnScan()

	case "VideoLibrary.OnCleanFinished":
		go library.RefreshOnClean()
	}
}
