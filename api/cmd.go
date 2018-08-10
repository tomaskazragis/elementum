package api

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
	"github.com/op/go-logging"

	"github.com/elgatito/elementum/cloudhole"
	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/library"
	"github.com/elgatito/elementum/xbmc"
)

var cmdLog = logging.MustGetLogger("cmd")

// ClearCache ...
func ClearCache(ctx *gin.Context) {
	key := ctx.Params.ByName("key")
	if ctx != nil {
		ctx.Abort()
	}
	if key != "" {
		library.ClearCacheKey(key)
	} else {
		library.ClearPageCache()
	}
	xbmc.Notify("Elementum", "LOCALIZE[30200]", config.AddonIcon())
}

// ClearPageCache ...
func ClearPageCache(ctx *gin.Context) {
	if ctx != nil {
		ctx.Abort()
	}
	library.ClearPageCache()
}

// ClearTraktCache ...
func ClearTraktCache(ctx *gin.Context) {
	if ctx != nil {
		ctx.Abort()
	}
	library.ClearTraktCache()
}

// ClearTmdbCache ...
func ClearTmdbCache(ctx *gin.Context) {
	if ctx != nil {
		ctx.Abort()
	}
	library.ClearTmdbCache()
}

// ResetClearances ...
func ResetClearances(ctx *gin.Context) {
	cloudhole.ResetClearances()
	xbmc.Notify("Elementum", "LOCALIZE[30264]", config.AddonIcon())
}

// ResetPath ...
func ResetPath(ctx *gin.Context) {
	xbmc.SetSetting("download_path", "")
	xbmc.SetSetting("library_path", "special://temp/elementum_library/")
	xbmc.SetSetting("torrents_path", "special://temp/elementum_torrents/")
}

// Pastebin uploads /debug/:type to pastebin
func Pastebin(ctx *gin.Context) {
	dialog := xbmc.NewDialogProgressBG("Elementum", "LOCALIZE[30457]", "LOCALIZE[30457]")
	if dialog != nil {
		dialog.Update(0, "Elementum", "LOCALIZE[30457]")
	}
	pasteURL := ""
	defer func() {
		if dialog != nil {
			dialog.Close()
		}

		if pasteURL != "" {
			xbmc.Dialog("Elementum", "LOCALIZE[30454];;"+pasteURL)
		}
	}()

	rtype := ctx.Params.ByName("type")
	rurl := fmt.Sprintf("http://%s:%d%s%s", config.Args.LocalHost, config.Args.LocalPort, "/debug/", rtype)

	log.Debugf("Requesting %s before uploading to pastebin", rurl)
	resp, err := http.Get(rurl)
	if err != nil {
		log.Debugf("Could not get %s: %#v", rurl, err)
		return
	}
	defer resp.Body.Close()
	content, _ := ioutil.ReadAll(resp.Body)

	values := url.Values{}
	values.Set("poster", "Elementum Uploader")
	values.Set("syntax", "text")
	values.Set("expiration", "")
	values.Set("content", string(content))

	response, err := http.PostForm("https://paste.ubuntu.com/", values)
	if err != nil {
		log.Noticef("Could not upload log file. Error: %#v", err)
		return
	} else if response != nil && response.StatusCode != 200 {
		log.Noticef("Could not upload log file. Status: %#v", response.StatusCode)
		return
	}

	defer response.Body.Close()

	pasteURL = response.Request.URL.String()
	log.Noticef("Log uploaded to: %s", pasteURL)
}

// SetViewMode ...
func SetViewMode(ctx *gin.Context) {
	contentType := ctx.Params.ByName("content_type")
	viewName := xbmc.InfoLabel("Container.Viewmode")
	viewMode := xbmc.GetCurrentView()
	cmdLog.Noticef("ViewMode: %s (%s)", viewName, viewMode)
	if viewMode != "0" {
		xbmc.SetSetting("viewmode_"+contentType, viewMode)
	}
	ctx.String(200, "")
}
