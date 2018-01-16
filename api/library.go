package api

import (
	"fmt"
	"strconv"

	"github.com/elgatito/elementum/bittorrent"
	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/library"
	"github.com/elgatito/elementum/xbmc"

	"github.com/gin-gonic/gin"
)

const (
	playLabel  = "LOCALIZE[30023]"
	linksLabel = "LOCALIZE[30202]"

	statusQueued      = "Queued"
	statusDownloading = "Downloading"
	statusSeeding     = "Seeding"
	statusFinished    = "Finished"
	statusPaused      = "Paused"
	statusFinding     = "Finding"
	statusBuffering   = "Buffering"
	statusAllocating  = "Allocating"
	statusStalled     = "Stalled"
	statusChecking    = "Checking"

	trueType  = "true"
	falseType = "false"

	movieType   = "movie"
	showType    = "show"
	episodeType = "episode"

	multiType = "\nmulti"
)

var (
	libraryPath       string
	moviesLibraryPath string
	showsLibraryPath  string
)

// AddMovie ...
func AddMovie(ctx *gin.Context) {
	tmdbID := ctx.Params.ByName("tmdbId")
	movie, err := library.AddMovie(tmdbID)
	if err != nil {
		ctx.String(200, err.Error())
		return
	}

	if xbmc.DialogConfirm("Elementum", fmt.Sprintf("LOCALIZE[30277];;%s", movie.Title)) {
		xbmc.VideoLibraryScan()
	} else {
		if ctx != nil {
			ctx.Abort()
		}
		library.ClearPageCache()
	}
}

// AddMoviesList ...
func AddMoviesList(ctx *gin.Context) {
	listID := ctx.Params.ByName("listId")
	updatingStr := ctx.DefaultQuery("updating", falseType)

	updating := false
	if updatingStr != falseType {
		updating = true
	}

	library.SyncMoviesList(listID, updating)
}

// RemoveMovie ...
func RemoveMovie(ctx *gin.Context) {
	tmdbID, _ := strconv.Atoi(ctx.Params.ByName("tmdbId"))
	movie, err := library.RemoveMovie(tmdbID)
	if err != nil {
		ctx.String(200, err.Error())
	}

	if ctx != nil {
		if movie != nil && xbmc.DialogConfirm("Elementum", fmt.Sprintf("LOCALIZE[30278];;%s", movie.Title)) {
			xbmc.VideoLibraryClean()
		} else {
			ctx.Abort()
			library.ClearPageCache()
		}
	}

}

//
// Shows externals
//

// AddShow ...
func AddShow(ctx *gin.Context) {
	tmdbID := ctx.Params.ByName("tmdbId")
	merge := ctx.DefaultQuery("merge", falseType)

	show, err := library.AddShow(tmdbID, merge)
	if err != nil {
		ctx.String(200, err.Error())
		return
	}

	label := "LOCALIZE[30277]"
	logMsg := "%s (%s) added to library"
	if merge == trueType {
		label = "LOCALIZE[30286]"
		logMsg = "%s (%s) merged to library"
	}

	log.Noticef(logMsg, show.Name, tmdbID)
	if xbmc.DialogConfirm("Elementum", fmt.Sprintf("%s;;%s", label, show.Name)) {
		xbmc.VideoLibraryScan()
	} else {
		library.ClearPageCache()
	}
}

// AddShowsList ...
func AddShowsList(ctx *gin.Context) {
	listID := ctx.Params.ByName("listId")
	updatingStr := ctx.DefaultQuery("updating", falseType)

	updating := false
	if updatingStr != falseType {
		updating = true
	}

	library.SyncShowsList(listID, updating)
}

// RemoveShow ...
func RemoveShow(ctx *gin.Context) {
	tmdbID := ctx.Params.ByName("tmdbId")
	show, err := library.RemoveShow(tmdbID)
	if err != nil {
		ctx.String(200, err.Error())
	}

	if ctx != nil {
		if show != nil && xbmc.DialogConfirm("Elementum", fmt.Sprintf("LOCALIZE[30278];;%s", show.Name)) {
			xbmc.VideoLibraryClean()
		} else {
			ctx.Abort()
			library.ClearPageCache()
		}
	}

}

// UpdateLibrary ...
func UpdateLibrary(ctx *gin.Context) {
	if err := library.Refresh(); err != nil {
		ctx.String(200, err.Error())
	}
	if xbmc.DialogConfirm("Elementum", "LOCALIZE[30288]") {
		xbmc.VideoLibraryScan()
	}
}

// UpdateTrakt ...
func UpdateTrakt(ctx *gin.Context) {
	if err := library.RefreshTrakt(); err != nil {
		ctx.String(200, err.Error())
	}
	if xbmc.DialogConfirm("Elementum", "LOCALIZE[30288]") {
		xbmc.VideoLibraryScan()
	}
}

// PlayMovie ...
func PlayMovie(btService *bittorrent.BTService) gin.HandlerFunc {
	if config.Get().ChooseStreamAuto == true {
		return MoviePlay(btService, true)
	}
	return MovieLinks(btService, true)
}

// PlayShow ...
func PlayShow(btService *bittorrent.BTService) gin.HandlerFunc {
	if config.Get().ChooseStreamAuto == true {
		return ShowEpisodePlay(btService, true)
	}
	return ShowEpisodeLinks(btService, true)
}
