package bittorrent

import (
	"fmt"
	"io"
	"strconv"  
	"strings"
	"net/http"
	"os"
	"time"

	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/xbmc"
)

// DebugBundle ...
func DebugBundle(s *BTService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logPath := xbmc.TranslatePath("special://logpath/kodi.log")
		logFile, err := os.Open(logPath)
		if err != nil {
			log.Debugf("Could not open kodi.log: %#v", err)
			return
		}
		defer logFile.Close()

		now := time.Now()
		fileName := fmt.Sprintf("bundle_%d_%d_%d_%d_%d.log", now.Year(), now.Month(), now.Day(), now.Hour(), now.Minute())
		w.Header().Set("Content-Disposition", "attachment; filename=" + fileName)
		w.Header().Set("Content-Type", "text/plain")

		
		writeHeader(w, "Torrent Client")
		writeResponse(w, "/info")

		writeHeader(w, "Debug LockTimes")
		writeResponse(w, "/debug/lockTimes")

		writeHeader(w, "Debug Vars")
		writeResponse(w, "/debug/vars")

		writeHeader(w, "Debug Perf")
		writeResponse(w, "/debug/perf")

		writeHeader(w, "kodi.log")
		io.Copy(w, logFile)
	})
}

func writeHeader(w http.ResponseWriter, title string) {
	w.Write([]byte("\n\n" + strings.Repeat("-", 40) + "\n"))
	w.Write([]byte(title))
	w.Write([]byte("\n" + strings.Repeat("-", 40) + "\n\n"))
}

func writeResponse(w http.ResponseWriter, url string) {
	w.Write([]byte("Response for url: " + url + "\n\n"))

	resp, err := http.Get("http://127.0.0.1:"+strconv.Itoa(config.ListenPort) + url)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	io.Copy(w, resp.Body)
}