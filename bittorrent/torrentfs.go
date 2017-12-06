package bittorrent

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/op/go-logging"
)

type TorrentFS struct {
	http.Dir
	service *BTService
}

var tfsLog = logging.MustGetLogger("torrentfs")

func TorrentFSHandler(btService *BTService, downloadPath string) http.Handler {
	return http.StripPrefix("/files", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Close = true
		entry, err := NewTorrentFS(btService, downloadPath, r)

		if err == nil && entry != nil {
			defer entry.Close()
			http.ServeContent(w, r, entry.File.DisplayPath(), time.Now(), entry.rs)
		} else {
			tfsLog.Noticef("Could not find torrent for requested file %s: %#v", r.URL.Path, err)
		}
	}))
}

func NewTorrentFS(service *BTService, path string, r *http.Request) (*FileEntry, error) {
	r.Close = true
	tfs := &TorrentFS{
		service: service,
		Dir:     http.Dir(path),
	}

	url := r.URL.Path
	if file, err := os.Open(filepath.Join(string(tfs.Dir), url)); err == nil {
		// make sure we don't open a file that's locked, as it can happen
		// on BSD systems (darwin included)
		if unlockerr := unlockFile(file); unlockerr != nil {
			tfsLog.Errorf("Unable to unlock file because: %s", unlockerr)
		}
	}

	tfsLog.Infof("Opening %s", url)
	for _, torrent := range tfs.service.Torrents {
		for _, f := range torrent.Files() {
			if url[1:] == f.Path() {
				tfsLog.Noticef("%s belongs to torrent %s", url, torrent.Name())
				if entry, createerr := NewFileReader(torrent, &f, !torrent.IsRarArchive, r.Method == "GET"); createerr == nil {
					torrent.GetDBID()
					return entry, nil
				}
			}
		}
	}

	return nil, errors.New("Could not find torrent handle for requested file path")
}
