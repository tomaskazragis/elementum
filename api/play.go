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

		if uri == "" && resume == "" {
			return
		}

		fileIndex := -1
		if index != "" {
			fIndex, err := strconv.Atoi(index)
			if err == nil {
				fileIndex = fIndex
			}
		}

		resumeIndex := -1
		if resume != "" {
			rIndex, err := strconv.Atoi(resume)
			if err == nil && rIndex >= 0 {
				resumeIndex = rIndex
			}
		}

		tmdbId := -1
		if tmdb != "" {
			id, err := strconv.Atoi(tmdb)
			if err == nil && id >= 0 {
				tmdbId = id
			}
		}

		params := bittorrent.BTPlayerParams{
			URI: uri,
			FileIndex: fileIndex,
			ResumeIndex: resumeIndex,
			ContentType: contentType,
			TMDBId: tmdbId,
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
