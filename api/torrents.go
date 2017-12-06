package api

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/cloudflare/ahocorasick"
	"github.com/dustin/go-humanize"
	"github.com/gin-gonic/gin"
	"github.com/op/go-logging"

	"github.com/elgatito/elementum/bittorrent"
	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/database"
	"github.com/elgatito/elementum/util"
	"github.com/elgatito/elementum/xbmc"
)

var torrentsLog = logging.MustGetLogger("torrents")
var cachedTorrents = map[int]string{}

type TorrentsWeb struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Size          string  `json:"size"`
	Status        string  `json:"status"`
	Progress      float64 `json:"progress"`
	Ratio         float64 `json:"ratio"`
	TimeRatio     float64 `json:"time_ratio"`
	SeedingTime   string  `json:"seeding_time"`
	SeedTime      float64 `json:"seed_time"`
	SeedTimeLimit int     `json:"seed_time_limit"`
	DownloadRate  float64 `json:"download_rate"`
	UploadRate    float64 `json:"upload_rate"`
	Seeders       int     `json:"seeders"`
	SeedersTotal  int     `json:"seeders_total"`
	Peers         int     `json:"peers"`
	PeersTotal    int     `json:"peers_total"`
}

var HistoryBucket = database.TorrentHistoryBucket

func AddToTorrentsMap(tmdbId string, torrent *bittorrent.TorrentFile) {
	b, err := ioutil.ReadFile(torrent.URI)
	if err != nil {
		return
	}

	torrentsLog.Debugf("Saving torrent entry for TMDB: %#v", tmdbId)
	db.SetBytes(HistoryBucket, tmdbId, b)
}

func InTorrentsMap(tmdbId string) (torrents []*bittorrent.TorrentFile) {
	if b, err := db.GetBytes(HistoryBucket, tmdbId); err == nil && len(b) > 0 {
		torrent := &bittorrent.TorrentFile{}
		torrent.LoadFromBytes(b)

		if len(torrent.URI) > 0 && xbmc.DialogConfirm("Elementum", fmt.Sprintf("LOCALIZE[30260];;[COLOR B8B8B800]%s[/COLOR]", torrent.Name)) {
			torrents = append(torrents, torrent)
		} else {
			db.Delete(HistoryBucket, tmdbId)
		}
	}

	return torrents
}

func nameMatch(torrentName string, itemName string) bool {
	patterns := strings.FieldsFunc(strings.ToLower(itemName), func(r rune) bool {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsMark(r) {
			return true
		}
		return false
	})

	m := ahocorasick.NewStringMatcher(patterns)

	found := m.Match([]byte(strings.ToLower(torrentName)))

	return len(found) >= len(patterns)
}

func ExistingTorrent(btService *bittorrent.BTService, longName string) (existingTorrent string) {
	for _, torrent := range btService.Torrents {
		if nameMatch(torrent.Name(), longName) {
			infoHash := torrent.InfoHash()
			torrentFile := filepath.Join(config.Get().TorrentsPath, fmt.Sprintf("%s.torrent", infoHash))
			torrentsLog.Debugf("Existing: %#v", torrentFile)
			return torrentFile
		}
	}

	return ""
}

func ListTorrents(btService *bittorrent.BTService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		items := make(xbmc.ListItems, 0, len(btService.Torrents))
		if len(btService.Torrents) == 0 {
			ctx.JSON(200, xbmc.NewView("", items))
			return
		}

		torrentsLog.Info("Currently downloading:")
		cachedTorrents = map[int]string{}
		counter := 0
		for i, torrent := range btService.Torrents {
			if torrent == nil {
				continue
			}

			torrentName := torrent.Name()
			progress := torrent.GetProgress()
			status := torrent.GetStateString()

			torrentAction := []string{"LOCALIZE[30231]", fmt.Sprintf("XBMC.RunPlugin(%s)", UrlForXBMC("/torrents/pause/%s", i))}
			sessionAction := []string{"LOCALIZE[30233]", fmt.Sprintf("XBMC.RunPlugin(%s)", UrlForXBMC("/torrents/pause"))}

			if status == "Paused" {
				sessionAction = []string{"LOCALIZE[30234]", fmt.Sprintf("XBMC.RunPlugin(%s)", UrlForXBMC("/torrents/resume"))}
			} else if status != "Finished" {
				if progress >= 100 {
					status = "Finished"
				} else {
					status = "Downloading"
				}
				torrentAction = []string{"LOCALIZE[30235]", fmt.Sprintf("XBMC.RunPlugin(%s)", UrlForXBMC("/torrents/resume/%s", i))}
			} else if status == "Finished" || progress >= 100 {
				status = "Seeding"
			}

			color := "white"
			switch status {
			case "Paused":
				fallthrough
			case "Finished":
				color = "grey"
			case "Seeding":
				color = "green"
			case "Buffering":
				color = "blue"
			case "Finding":
				color = "orange"
			case "Checking":
				color = "teal"
			case "Queued":
			case "Allocating":
				color = "black"
			case "Stalled":
				color = "red"
			}

			// TODO: Add seeding time and ratio getter/output
			torrentsLog.Infof("- %.2f%% - %s - %s", progress, status, torrentName)

			var (
				tmdb        string
				show        string
				season      string
				episode     string
				contentType string
			)

			if torrent.DBItem != nil && torrent.DBItem.Type != "" {
				contentType = torrent.DBItem.Type
				if contentType == "movie" {
					tmdb = strconv.Itoa(torrent.DBItem.ID)
				} else {
					show = strconv.Itoa(torrent.DBItem.ShowID)
					season = strconv.Itoa(torrent.DBItem.Season)
					episode = strconv.Itoa(torrent.DBItem.Episode)
				}
			}

			cachedTorrents[counter] = torrent.InfoHash()
			playUrl := UrlQuery(UrlForXBMC("/play"),
				"resume", i,
				"type", contentType,
				"tmdb", tmdb,
				"show", show,
				"season", season,
				"episode", episode)

			item := xbmc.ListItem{
				Label: fmt.Sprintf("%.2f%% - [COLOR %s]%s[/COLOR] - %s", progress, color, status, torrentName),
				Path:  playUrl,
				Info: &xbmc.ListItemInfo{
					Title: torrentName,
				},
			}
			item.ContextMenu = [][]string{
				[]string{"LOCALIZE[30230]", fmt.Sprintf("XBMC.PlayMedia(%s)", playUrl)},
				torrentAction,
				[]string{"LOCALIZE[30232]", fmt.Sprintf("XBMC.RunPlugin(%s)", UrlForXBMC("/torrents/delete/%s", i))},
				[]string{"LOCALIZE[30276]", fmt.Sprintf("XBMC.RunPlugin(%s)", UrlForXBMC("/torrents/delete/%s?files=1", i))},
				[]string{"LOCALIZE[30308]", fmt.Sprintf("XBMC.RunPlugin(%s)", UrlForXBMC("/torrents/move/%s", i))},
				sessionAction,
			}
			item.IsPlayable = true
			items = append(items, &item)
		}

		ctx.JSON(200, xbmc.NewView("", items))
	}
}

func ListTorrentsWeb(btService *bittorrent.BTService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		torrents := make([]*TorrentsWeb, 0, len(btService.Torrents))

		if len(btService.Torrents) == 0 {
			ctx.Writer.Header().Set("Access-Control-Allow-Origin", "*")
			ctx.JSON(200, torrents)
			return
		}

		torrentsLog.Info("Currently downloading:")
		cachedTorrents = map[int]string{}
		counter := 0
		for _, torrent := range btService.Torrents {
			if torrent == nil {
				continue
			}

			torrentName := torrent.Name()
			progress := torrent.GetProgress()
			status := torrent.GetStateString()

			if status != "Finished" {
				if progress >= 100 {
					status = "Finished"
				} else {
					status = "Downloading"
				}
			} else if status == "Finished" || progress >= 100 {
				status = "Seeding"
			}

			size := humanize.Bytes(uint64(torrent.Length()))
			downloadRate := float64(torrent.DownloadRate) / 1024
			uploadRate := float64(torrent.UploadRate) / 1024

			stats := torrent.Stats()
			peers := stats.ActivePeers
			peersTotal := stats.TotalPeers

			cachedTorrents[counter] = torrent.InfoHash()
			t := TorrentsWeb{
				ID:           torrent.InfoHash(),
				Name:         torrentName,
				Size:         size,
				Status:       status,
				Progress:     progress,
				DownloadRate: downloadRate,
				UploadRate:   uploadRate,
				Peers:        peers,
				PeersTotal:   peersTotal,
			}
			torrents = append(torrents, &t)
			counter++
		}

		ctx.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		ctx.JSON(200, torrents)
	}
}

func PauseSession(btService *bittorrent.BTService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		// TODO: Add Global Pause
		xbmc.Refresh()
		ctx.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		ctx.String(200, "")
	}
}

func ResumeSession(btService *bittorrent.BTService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		// TODO: Add Global Resume
		xbmc.Refresh()
		ctx.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		ctx.String(200, "")
	}
}

func AddTorrent(btService *bittorrent.BTService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		uri := ctx.Query("uri")
		ctx.Writer.Header().Set("Access-Control-Allow-Origin", "*")

		if uri == "" {
			ctx.String(404, "Missing torrent URI")
			return
		}
		torrentsLog.Infof("Adding torrent from %s", uri)

		if config.Get().DownloadPath == "." {
			xbmc.Notify("Elementum", "LOCALIZE[30113]", config.AddonIcon())
			ctx.String(404, "Download path empty")
			return
		}

		_, err := btService.AddTorrent(uri)
		if err != nil {
			ctx.String(404, err.Error())
			return
		}

		torrentsLog.Infof("Downloading %s", uri)

		xbmc.Refresh()
		ctx.String(200, "")
	}
}

func ResumeTorrent(btService *bittorrent.BTService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		torrentId := ctx.Params.ByName("torrentId")
		torrent, err := GetTorrentFromParam(btService, torrentId)
		if err != nil {
			ctx.Error(errors.New(fmt.Sprintf("Unable to resume torrent with index %s", torrentId)))
			return
		}

		torrent.Resume()

		xbmc.Refresh()
		ctx.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		ctx.String(200, "")
	}
}

func MoveTorrent(btService *bittorrent.BTService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		torrentId := ctx.Params.ByName("torrentId")
		torrent, err := GetTorrentFromParam(btService, torrentId)
		if err != nil {
			ctx.Error(errors.New(fmt.Sprintf("Unable to move torrent with index %s", torrentId)))
			return
		}

		torrentsLog.Infof("Marking %s to be moved...", torrent.Name())
		btService.MarkedToMove = torrent.InfoHash()

		xbmc.Refresh()
		ctx.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		ctx.String(200, "")
	}
}

func PauseTorrent(btService *bittorrent.BTService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		torrentId := ctx.Params.ByName("torrentId")
		torrent, err := GetTorrentFromParam(btService, torrentId)
		if err != nil {
			ctx.Error(errors.New(fmt.Sprintf("Unable to pause torrent with index %s", torrentId)))
			return
		}

		torrent.Pause()

		xbmc.Refresh()
		ctx.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		ctx.String(200, "")
	}
}

func RemoveTorrent(btService *bittorrent.BTService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		deleteFiles := ctx.Query("files")

		torrentId := ctx.Params.ByName("torrentId")
		torrent, err := GetTorrentFromParam(btService, torrentId)
		if err != nil {
			ctx.Error(errors.New(fmt.Sprintf("Unable to remove torrent with index %s", torrentId)))
			return
		}

		// Delete torrent file
		torrentsPath := config.Get().TorrentsPath
		infoHash := torrent.InfoHash()
		torrentFile := filepath.Join(torrentsPath, fmt.Sprintf("%s.torrent", infoHash))
		if _, err := os.Stat(torrentFile); err == nil {
			torrentsLog.Infof("Deleting torrent file at %s", torrentFile)
			defer os.Remove(torrentFile)
		}

		btService.UpdateDB(bittorrent.Delete, infoHash, 0, "")
		torrentsLog.Infof("Removed %s from database", infoHash)

		keepSetting := config.Get().KeepFilesFinished
		deleteAnswer := false
		if keepSetting == 1 && deleteFiles == "" && xbmc.DialogConfirm("Elementum", "LOCALIZE[30269]") {
			deleteAnswer = true
		} else if keepSetting == 2 {
			deleteAnswer = true
		}

		if deleteAnswer == true || deleteFiles == "true" {
			torrentsLog.Info("Removing the torrent and deleting files...")
			btService.RemoveTorrent(torrent, true)
		} else {
			torrentsLog.Info("Removing the torrent without deleting files...")
			btService.RemoveTorrent(torrent, false)
		}

		xbmc.Refresh()
		ctx.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		ctx.String(200, "")
	}
}

func Versions(btService *bittorrent.BTService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		type Versions struct {
			Version   string `json:"version"`
			UserAgent string `json:"user-agent"`
		}
		versions := Versions{
			Version:   util.Version[1 : len(util.Version)-1],
			UserAgent: btService.UserAgent,
		}
		ctx.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		ctx.JSON(200, versions)
	}
}

func GetTorrentFromParam(btService *bittorrent.BTService, param string) (*bittorrent.Torrent, error) {
	if len(param) == 0 {
		return nil, errors.New("Empty param")
	}

	if len(param) < 5 {
		id, err := strconv.Atoi(param)
		if err != nil {
			return nil, errors.New("Wrong int param")
		}

		if v, ok := cachedTorrents[id]; !ok {
			return nil, errors.New("Wrong int index")
		} else {
			param = v
		}
	}

	if t, ok := btService.Torrents[param]; !ok {
		return nil, errors.New("Torrent not found")
	} else {
		return t, nil
	}
}
