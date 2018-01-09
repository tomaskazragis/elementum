package api

import (
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"

	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/xbmc"
)

// Changelog display
func Changelog(ctx *gin.Context) {
	changelogPath := filepath.Join(config.Get().Info.Path, "whatsnew.txt")
	if _, err := os.Stat(changelogPath); err != nil {
		ctx.String(404, err.Error())
		return
	}

	title := "LOCALIZE[30355]"
	text, err := ioutil.ReadFile(changelogPath)
	if err != nil {
		ctx.String(404, err.Error())
		return
	}

	xbmc.DialogText(title, string(text))
	ctx.String(200, "")
}
