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
func Notification(w http.ResponseWriter, r *http.Request, s *bittorrent.BTService) {
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

	case "Player.OnPause":
		p := s.GetActivePlayer()
		if p == nil || p.Params().VideoDuration == 0 {
			return
		}

		if !p.Params().Paused {
			p.Params().Paused = true
		}

	case "Player.OnPlay":
		time.Sleep(1 * time.Second) // Let player get its WatchedTime and VideoDuration
		p := s.GetActivePlayer()
		if p == nil || p.Params().VideoDuration == 0 {
			return
		}

		if p.Params().Paused { // Prevent seeking when simply unpausing
			p.Params().Paused = false
			return
		}
		if !p.Params().FromLibrary || !config.Get().PlayResume || p.Params().KodiPosition == 0 {
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
		var position float64
		uids := library.GetUIDsFromKodi(started.Item.ID)
		if uids == nil {
			log.Warningf("No item found with ID: %d", started.Item.ID)
			return
		}

		if started.Item.Type == movieType {
			r := library.GetMovieResume(started.Item.ID)
			if r == nil {
				return
			}
			position = r.Position
		} else {
			r := library.GetEpisodeResume(started.Item.ID)
			if r == nil {
				return
			}
			position = r.Position
		}
		if position > 0 {
			xbmc.PlayerSeek(position)
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
			library.RefreshMovie(item.ID, library.ActionDelete)
		} else if item.Type == showType {
			library.RefreshShow(item.ID, library.ActionDelete)
		} else if item.Type == episodeType {
			library.RefreshEpisode(item.ID, library.ActionDelete)
		}

	case "VideoLibrary.OnScanFinished":
		go library.RefreshOnScan()

	case "VideoLibrary.OnCleanFinished":
		go library.RefreshOnClean()
	}
}
