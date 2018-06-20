package api

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/elgatito/elementum/database"

	"github.com/dustin/go-humanize"
	"github.com/gin-gonic/gin"

	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/util"
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

// Status display
func Status(ctx *gin.Context) {
	title := "LOCALIZE[30393]"
	text := ""

	text += `[B]LOCALIZE[30394]:[/B] %s

[B]LOCALIZE[30395]:[/B] %s
[B]LOCALIZE[30396]:[/B] %d

[COLOR pink][B]LOCALIZE[30399]:[/B][/COLOR]
    [B]LOCALIZE[30397]:[/B] %s
    [B]LOCALIZE[30401]:[/B] %s
    [B]LOCALIZE[30398]:[/B] %s

[COLOR pink][B]LOCALIZE[30400]:[/B][/COLOR]
    [B]LOCALIZE[30403]:[/B] %s
    [B]LOCALIZE[30402]:[/B] %s

    [B]LOCALIZE[30404]:[/B] %d
    [B]LOCALIZE[30405]:[/B] %d
`

	ip, _ := util.LocalIP()
	port := config.Args.LocalPort
	webAddress := fmt.Sprintf("http://%s:%d/web", ip.String(), port)
	debugAddress := fmt.Sprintf("http://%s:%d/debug/bundle", ip.String(), port)
	infoAddress := fmt.Sprintf("http://%s:%d/info", ip.String(), port)

	appSize := fileSize(filepath.Join(config.Get().Info.Profile, database.Get().GetFilename()))
	cacheSize := fileSize(filepath.Join(config.Get().Info.Profile, database.GetCache().GetFilename()))

	torrentsCount := 0
	database.Get().QueryRow("SELECT COUNT(1) FROM thistory_metainfo").Scan(&torrentsCount)

	queriesCount := 0
	database.Get().QueryRow("SELECT COUNT(1) FROM history_queries").Scan(&queriesCount)

	text = fmt.Sprintf(text,
		util.GetVersion(),
		ip,
		port,

		webAddress,
		infoAddress,
		debugAddress,

		appSize,
		cacheSize,

		torrentsCount,
		queriesCount,
	)

	xbmc.DialogText(title, string(text))
	ctx.String(200, "")
}

func fileSize(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}

	return humanize.Bytes(uint64(fi.Size()))
}
