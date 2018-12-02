package api

import (
	"fmt"
	"net/url"
	"strconv"

	"github.com/elgatito/elementum/bittorrent"
	"github.com/elgatito/elementum/database"
	"github.com/elgatito/elementum/util"
	"github.com/elgatito/elementum/xbmc"

	"github.com/gin-gonic/gin"
)

// Play ...
func Play(btService *bittorrent.BTService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		uri := ctx.Query("uri")
		index := ctx.Query("index")
		resume := ctx.Query("resume")
		query := ctx.Query("query")
		contentType := ctx.Query("type")
		tmdb := ctx.Query("tmdb")
		show := ctx.Query("show")
		season := ctx.Query("season")
		episode := ctx.Query("episode")

		if uri == "" && resume == "" {
			return
		}

		fileIndex := -1
		if index != "" {
			if position, err := strconv.Atoi(index); err == nil && position >= 0 {
				fileIndex = position
			}
		}

		resumeIndex := -1
		if resume != "" {
			if position, err := strconv.Atoi(resume); err == nil && position >= 0 {
				resumeIndex = position
			}
		}

		tmdbID := 0
		if tmdb != "" {
			if id, err := strconv.Atoi(tmdb); err == nil && id > 0 {
				tmdbID = id
			}
		}

		showID := 0
		if show != "" {
			if id, err := strconv.Atoi(show); err == nil && id > 0 {
				showID = id
			}
		}

		seasonNumber := 0
		if season != "" {
			if number, err := strconv.Atoi(season); err == nil && number > 0 {
				seasonNumber = number
			}
		}

		episodeNumber := 0
		if episode != "" {
			if number, err := strconv.Atoi(episode); err == nil && number > 0 {
				episodeNumber = number
			}
		}

		params := bittorrent.PlayerParams{
			URI:          uri,
			FileIndex:    fileIndex,
			ResumeIndex:  resumeIndex,
			KodiPosition: -1,
			ContentType:  contentType,
			TMDBId:       tmdbID,
			ShowID:       showID,
			Season:       seasonNumber,
			Episode:      episodeNumber,
			Query:        query,
		}

		player := bittorrent.NewBTPlayer(btService, params)
		log.Debugf("Playing item: %#v", resume)
		if t, ok := btService.Torrents[resume]; resume != "" && ok {
			player.Torrent = t
		}
		if player.Buffer() != nil || !player.HasChosenFile() {
			player.Close()
			return
		}

		rURL, _ := url.Parse(fmt.Sprintf("%s/files/%s", util.GetContextHTTPHost(ctx), player.PlayURL()))
		ctx.Redirect(302, rURL.String())
	}
}

// PlayTorrent ...
func PlayTorrent(ctx *gin.Context) {
	retval := xbmc.DialogInsert()
	if retval["path"] == "" {
		return
	}
	go xbmc.PlayURLWithTimeout(URLQuery(URLForXBMC("/play"), "uri", retval["path"]))

	ctx.String(200, "")
	return
}

// PlayURI ...
func PlayURI(btService *bittorrent.BTService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		uri := ctx.Request.FormValue("uri")
		file, header, fileError := ctx.Request.FormFile("file")

		if file != nil && header != nil && fileError == nil {
			t, err := saveTorrentFile(file, header)
			if err == nil && t != "" {
				uri = t
			}
		}

		index := ctx.Query("index")
		resume := ctx.Query("resume")

		if uri == "" && resume == "" {
			return
		}

		if uri != "" {
			xbmc.PlayURL(URLQuery(URLForXBMC("/play"), "uri", uri, "index", index))
		} else {
			var (
				tmdb        string
				show        string
				season      string
				episode     string
				query       string
				contentType string
			)
			torrentHandle := btService.Torrents[resume]

			if torrentHandle != nil {
				infoHash := torrentHandle.Torrent.InfoHash().AsString()
				dbItem := database.Get().GetBTItem(infoHash)
				if dbItem != nil && dbItem.Type != "" {
					contentType = dbItem.Type
					if contentType == movieType {
						tmdb = strconv.Itoa(dbItem.ID)
					} else {
						show = strconv.Itoa(dbItem.ShowID)
						season = strconv.Itoa(dbItem.Season)
						episode = strconv.Itoa(dbItem.Episode)
					}
					query = dbItem.Query
				}
			}
			xbmc.PlayURL(URLQuery(URLForXBMC("/play"),
				"resume", resume,
				"index", index,
				"tmdb", tmdb,
				"show", show,
				"season", season,
				"episode", episode,
				"query", query,
				"type", contentType))
		}
		ctx.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		ctx.String(200, "")
	}
}
