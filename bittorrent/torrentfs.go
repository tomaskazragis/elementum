package bittorrent

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/anacrolix/missinggo"
	"github.com/anacrolix/missinggo/httptoo"
	"github.com/op/go-logging"

	"github.com/elgatito/elementum/util"
)

var tfsLog = logging.MustGetLogger("torrentfs")

// ServeTorrent ...
func ServeTorrent(s *BTService, downloadPath string) http.Handler {
	return http.StripPrefix("/files", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "close")
		r.Close = true
		url := r.URL.Path

		fr, err := GetTorrentForPath(s, downloadPath, url, r)
		if err != nil || fr == nil {
			http.Error(w, "file not found", http.StatusNotFound)
			return
		}
		w.Header().Set("ETag", httptoo.EncodeQuotedString(fmt.Sprintf("%s/%s", fr.Torrent.infoHash, url[1:])))

		rs := missinggo.NewSectionReadSeeker(struct {
			io.Reader
			io.Seeker
		}{
			Reader: missinggo.ContextedReader{
				R:   fr.Reader,
				Ctx: r.Context(),
			},
			Seeker: fr.Reader,
		}, 0, fr.File.Length())

		defer fr.Close()
		http.ServeContent(w, r, url, time.Time{}, rs)
	}))
}

// GetTorrentForPath ...
func GetTorrentForPath(s *BTService, upath string, url string, r *http.Request) (*FileReader, error) {
	path := util.DecodeFileURL(upath)
	dir := string(http.Dir(path))
	if file, err := os.Open(filepath.Join(dir, url)); err == nil {
		// make sure we don't open a file that's locked, as it can happen
		// on BSD systems (darwin included)
		if unlockerr := unlockFile(file); unlockerr != nil {
			tfsLog.Errorf("Unable to unlock file because: %s", unlockerr)
		}
	}

	tfsLog.Infof("Opening %s", url)
	for _, torrent := range s.Torrents {
		for _, f := range torrent.Files() {
			if url[1:] == f.Path() {
				tfsLog.Noticef("%s belongs to torrent %s", url, torrent.Name())
				return NewFileReader(torrent, f, r.Method)
			}
		}
	}

	return nil, errors.New("Could not find torrent handle for requested file path")
}
