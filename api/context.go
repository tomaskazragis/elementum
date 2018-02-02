package api

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/elgatito/elementum/bittorrent"
	"github.com/elgatito/elementum/library"
)

// ContextPlaySelector ...
func ContextPlaySelector(btService *bittorrent.BTService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		ctx.Writer.Header().Set("Access-Control-Allow-Origin", "*")

		kodiID, _ := strconv.Atoi(ctx.Params.ByName("kodiID"))
		media := ctx.Params.ByName("media")

		if media == "movie" {
			if m := library.GetLibraryMovie(kodiID); m != nil && m.UIDs.TMDB != 0 {
				ctx.Redirect(302, URLQuery(URLForXBMC("/movie/%d/forceplay", m.UIDs.TMDB)))
				return
			}
		} else if media == "episode" {
			if s, e := library.GetLibraryEpisode(kodiID); s != nil && e != nil && e.UIDs.TMDB != 0 {
				ctx.Redirect(302, URLQuery(URLForXBMC("/show/%d/season/%d/episode/%d/forceplay", s.UIDs.TMDB, e.Season, e.Episode)))
				return
			}
		}

		log.Debugf("Cound not find TMDB entry for requested Kodi item %d of type %s", kodiID, media)
		ctx.String(404, "Cannot find TMDB for selected Kodi item")
		return
	}
}
