package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/elgatito/elementum/bittorrent"
	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/library"
	"github.com/elgatito/elementum/trakt"
	"github.com/elgatito/elementum/xbmc"
)

const (
	movieType   = "movie"
	showType    = "show"
	seasonType  = "season"
	episodeType = "episode"
)

// Notification serves callbacks from Kodi
func Notification(w http.ResponseWriter, r *http.Request, s *bittorrent.BTService) {
	sender := r.URL.Query().Get("sender")
	method := r.URL.Query().Get("method")
	data := r.URL.Query().Get("data")

	jsonData, _ := base64.StdEncoding.DecodeString(data)
	log.Debugf("Got notification from %s/%s: %s", sender, method, string(jsonData))

	if sender != "xbmc" {
		return
	}

	switch method {
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
		if !p.Params().FromLibrary {
			return
		}
		libraryResume := config.Get().LibraryResume
		if libraryResume == 0 {
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
			movie := library.GetMovie(started.Item.ID)
			if movie == nil || (libraryResume == 2 && s.HasTorrent(uids.TMDB)) {
				return
			}
			position = movie.Resume.Position
		} else {
			episode := library.GetEpisode(started.Item.ID)
			if episode == nil || (libraryResume == 2 && s.HasTorrent(uids.TMDB)) {
				return
			}
			position = episode.Resume.Position
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

		if stopped.Ended && progress > 90 {
			if stopped.Item.Type == movieType {
				xbmc.SetMovieWatched(stopped.Item.ID, 1, 0, 0)
			} else {
				xbmc.SetEpisodeWatched(stopped.Item.ID, 1, 0, 0)
			}
		} else if p.Params().WatchedTime > 180 {
			if stopped.Item.Type == movieType {
				xbmc.SetMovieWatched(stopped.Item.ID, 0, int(p.Params().WatchedTime), int(p.Params().VideoDuration))
			} else {
				xbmc.SetEpisodeWatched(stopped.Item.ID, 0, int(p.Params().WatchedTime), int(p.Params().VideoDuration))
			}
		} else {
			time.Sleep(200 * time.Millisecond)
			xbmc.Refresh()
		}

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
		if err := json.Unmarshal(jsonData, &request); err != nil {
			log.Error(err)
			return
		}

		if config.Get().TraktToken != "" && !library.TraktScanning {
			var watched *trakt.WatchedItem
			if request.Item.Type == movieType {
				movie := library.GetLibraryMovie(request.Item.ID)
				if movie != nil {
					watched = &trakt.WatchedItem{
						MediaType: request.Item.Type,
						Movie:     movie.UIDs.TMDB,
					}
				}
			} else if request.Item.Type == showType {
				show := library.GetLibraryShow(request.Item.ID)
				if show != nil {
					watched = &trakt.WatchedItem{
						MediaType: request.Item.Type,
						Show:      show.UIDs.TMDB,
					}
				}
			} else if request.Item.Type == seasonType {
				show, season := library.GetLibrarySeason(request.Item.ID)
				if show != nil && season != nil {
					watched = &trakt.WatchedItem{
						MediaType: request.Item.Type,
						Show:      show.UIDs.TMDB,
						Season:    season.Season,
					}
				}
			} else if request.Item.Type == episodeType {
				show, episode := library.GetLibraryEpisode(request.Item.ID)
				if show != nil && episode != nil {
					watched = &trakt.WatchedItem{
						MediaType: request.Item.Type,
						Show:      show.UIDs.TMDB,
						Season:    episode.Season,
						Episode:   episode.Episode,
					}
				}
			}

			if watched != nil {
				watched.Watched = request.Playcount > 0
				go trakt.SetWatched(watched)
			}
		}

		if request.Item.ID == 0 || library.TraktScanning || library.Scanning {
			return
		}

		if request.Item.Type == movieType {
			library.RefreshMovies()
		} else {
			library.RefreshShows()
		}
		xbmc.Refresh()

	case "VideoLibrary.OnRemove":
		var item struct {
			ID   int    `json:"id"`
			Type string `json:"type"`
		}
		if err := json.Unmarshal(jsonData, &item); err != nil {
			log.Error(err)
			return
		}

		uids := library.GetUIDsFromKodi(item.ID)
		if uids == nil || uids.TMDB == 0 {
			return
		}

		switch item.Type {
		case episodeType:
			show, episode := library.GetShowForEpisode(item.ID)
			if show != nil && episode != nil {
				if err := library.RemoveEpisode(uids.TMDB, show.UIDs.TMDB, strconv.Itoa(show.UIDs.TMDB), episode.Season, episode.Episode); err != nil {
					log.Warning(err)
				}
			}
		case movieType:
			if _, err := library.RemoveMovie(uids.TMDB); err != nil {
				log.Warning("Nothing left to remove from Elementum")
			}
		}

	case "VideoLibrary.OnScanFinished":
		// library.Scanning = false
		fallthrough

	case "VideoLibrary.OnCleanFinished":
		go func() {
			library.Refresh()
			library.ClearPageCache()
		}()
	}
}
