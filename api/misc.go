package api

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/elgatito/elementum/database"
	"github.com/elgatito/elementum/library"

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
    [B]LOCALIZE[30439]:[/B] %s
    [B]LOCALIZE[30398]:[/B] %s

[COLOR pink][B]LOCALIZE[30400]:[/B][/COLOR]
    [B]LOCALIZE[30403]:[/B] %s
    [B]LOCALIZE[30402]:[/B] %s

    [B]LOCALIZE[30404]:[/B] %d
    [B]LOCALIZE[30405]:[/B] %d
    [B]LOCALIZE[30458]:[/B] %d
    [B]LOCALIZE[30459]:[/B] %d
`

	ip := "127.0.0.1"
	if localIP, err := util.LocalIP(); err == nil {
		ip = localIP.String()
	}

	port := config.Args.LocalPort
	webAddress := fmt.Sprintf("http://%s:%d/web", ip, port)
	debugAllAddress := fmt.Sprintf("http://%s:%d/debug/all", ip, port)
	debugBundleAddress := fmt.Sprintf("http://%s:%d/debug/bundle", ip, port)
	infoAddress := fmt.Sprintf("http://%s:%d/info", ip, port)

	appSize := fileSize(filepath.Join(config.Get().Info.Profile, database.Get().GetFilename()))
	cacheSize := fileSize(filepath.Join(config.Get().Info.Profile, database.GetCache().GetFilename()))

	torrentsCount := 0
	queriesCount := 0
	deletedMoviesCount := 0
	deletedShowsCount := 0

	database.Get().QueryRow("SELECT COUNT(1) FROM thistory_metainfo").Scan(&torrentsCount)
	database.Get().QueryRow("SELECT COUNT(1) FROM history_queries").Scan(&queriesCount)
	database.Get().QueryRow("SELECT COUNT(1) FROM library_items WHERE state = ? AND mediaType = ?", library.StateDeleted, library.MovieType).Scan(&deletedMoviesCount)
	database.Get().QueryRow("SELECT COUNT(1) FROM library_items WHERE state = ? AND mediaType = ?", library.StateDeleted, library.ShowType).Scan(&deletedShowsCount)

	text = fmt.Sprintf(text,
		util.GetVersion(),
		ip,
		port,

		webAddress,
		infoAddress,
		debugAllAddress,
		debugBundleAddress,

		appSize,
		cacheSize,

		torrentsCount,
		queriesCount,
		deletedMoviesCount,
		deletedShowsCount,
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
