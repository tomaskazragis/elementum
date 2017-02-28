package api

import (
	"fmt"
	"net/url"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/scakemyer/quasar/bittorrent"
	"github.com/scakemyer/quasar/util"
	"github.com/scakemyer/quasar/xbmc"
)

func Play(btService *bittorrent.BTService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		uri := ctx.Query("uri")
		index := ctx.Query("index")
		resume := ctx.Query("resume")
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

		tmdbId := 0
		if tmdb != "" {
			if id, err := strconv.Atoi(tmdb); err == nil && id > 0 {
				tmdbId = id
			}
		}

		showId := 0
		if show != "" {
			if id, err := strconv.Atoi(show); err == nil && id > 0 {
				showId = id
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

		params := bittorrent.BTPlayerParams{
			URI: uri,
			FileIndex: fileIndex,
			ResumeIndex: resumeIndex,
			ContentType: contentType,
			TMDBId: tmdbId,
			ShowID: showId,
			Season: seasonNumber,
			Episode: episodeNumber,
		}

		player := bittorrent.NewBTPlayer(btService, params)
		if player.Buffer() != nil {
			return
		}

		rUrl, _ := url.Parse(fmt.Sprintf("%s/files/%s", util.GetHTTPHost(), player.PlayURL()))
		ctx.Redirect(302, rUrl.String())
	}
}

func PlayTorrent(ctx *gin.Context) {
	retval := xbmc.DialogInsert()
	if retval["path"] == "" {
		return
	}
	xbmc.PlayURL(UrlQuery(UrlForXBMC("/play"), "uri", retval["path"]))
}

func PlayURI(ctx *gin.Context) {
	ctx.Writer.Header().Set("Access-Control-Allow-Origin", "*")
	uri := ctx.Query("uri")
	resume := ctx.Query("resume")

	if uri == "" && resume == "" {
		return
	}

	if uri != "" {
		xbmc.PlayURL(UrlQuery(UrlForXBMC("/play"), "uri", uri))
	} else {
		xbmc.PlayURL(UrlQuery(UrlForXBMC("/play"), "resume", resume))
	}
	ctx.String(200, "")
}
