package api

import (
	"github.com/elgatito/elementum/youtube"
	"github.com/gin-gonic/gin"
)

// PlayYoutubeVideo ...
func PlayYoutubeVideo(ctx *gin.Context) {
	youtubeID := ctx.Params.ByName("id")
	streams, err := youtube.Resolve(youtubeID)
	if err != nil {
		ctx.String(200, err.Error())
	}
	for _, stream := range streams {
		ctx.Redirect(302, stream)
		return
	}
}
