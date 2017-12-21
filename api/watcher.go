package api

import (
	"strconv"

	"github.com/elgatito/elementum/bittorrent"
	"github.com/elgatito/elementum/broadcast"
	"github.com/op/go-logging"
)

var (
	watcherLog = logging.MustGetLogger("watcher")
)

// LibraryListener ...
func LibraryListener() {
	broadcaster := broadcast.LocalBroadcasters[broadcast.WATCHED]

	c, done := broadcaster.Listen()
	defer close(done)

	for {
		select {
		case v, ok := <-c:
			if !ok {
				return
			}

			updateWatchedForItem(v.(*bittorrent.PlayingItem))
		}
	}
}

func updateWatchedForItem(item *bittorrent.PlayingItem) {
	if item.Duration == 0 || item.WatchedTime == 0 {
		return
	}

	if item.DBTYPE == movieType {
		if item.DBID == 0 {
			xbmcItem := FindByIDMovieInLibrary(strconv.Itoa(item.TMDBID))
			if xbmcItem != nil {
				item.DBID = xbmcItem.ID
			}
		}

		if item.DBID != 0 {
			UpdateMovieWatched(item.DBID, item.WatchedTime, item.Duration)
		}
	} else if item.DBTYPE == episodeType {
		if item.DBID == 0 {
			xbmcItem := FindByIDEpisodeInLibrary(item.TMDBID, item.Season, item.Episode)
			if xbmcItem != nil {
				item.DBID = xbmcItem.ID
			}
		}

		if item.DBID != 0 {
			UpdateEpisodeWatched(item.DBID, item.WatchedTime, item.Duration)
		}
	}
}
