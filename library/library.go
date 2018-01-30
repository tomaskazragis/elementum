package library

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cespare/xxhash"
	"github.com/op/go-logging"

	"github.com/elgatito/elementum/cache"
	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/database"
	"github.com/elgatito/elementum/playcount"
	"github.com/elgatito/elementum/tmdb"
	"github.com/elgatito/elementum/trakt"
	"github.com/elgatito/elementum/util"
	"github.com/elgatito/elementum/xbmc"
)

const (
	movieType   = "movie"
	showType    = "show"
	episodeType = "episode"

	trueType  = "true"
	falseType = "false"

	resolveExpiration     = 7 * 24 * time.Hour
	resolveFileExpiration = 14 * 24 * time.Hour
)

const (
	// MovieType ...
	MovieType = iota
	// ShowType ...
	ShowType
	// SeasonType ...
	SeasonType
	// EpisodeType ...
	EpisodeType
	// RemovedMovieType ...
	RemovedMovieType
	// RemovedShowType ...
	RemovedShowType
	// RemovedSeasonType ...
	RemovedSeasonType
	// RemovedEpisodeType ...
	RemovedEpisodeType
)

const (
	// TVDBScraper ...
	TVDBScraper = iota
	// TMDBScraper ...
	TMDBScraper
	// TraktScraper ...
	TraktScraper
	// IMDBScraper ...
	IMDBScraper
)

const (
	// Delete ...
	Delete = iota
	// Update ...
	Update
	// Batch ...
	Batch
	// BatchDelete ...
	BatchDelete
	// DeleteTorrent ...
	DeleteTorrent
)

var (
	closing         = make(chan struct{})
	removedEpisodes = make(chan *removedEpisode)

	log = logging.MustGetLogger("library")

	libraryPath       string
	moviesLibraryPath string
	showsLibraryPath  string

	db       *database.Database
	dbBucket = database.LibraryBucket

	cacheStore *cache.DBStore

	// Scanning shows if Kodi library Scan is in progress
	Scanning = false
	// TraktScanning shows if Trakt is working
	TraktScanning = false

	resolveRegexp = regexp.MustCompile(`^plugin://plugin.video.elementum.*?(\d+)(\W|$)`)
)

var l = &Library{
	UIDs:   map[int]*UniqueIDs{},
	Movies: map[int]*Movie{},
	Shows:  map[int]*Show{},

	WatchedTrakt: map[uint64]bool{},
}

// InitDB ...
func InitDB() {
	db = database.Get()
	cacheStore = cache.NewDBStore()
}

// Get returns singleton instance for Library
func Get() *Library {
	return l
}

// Init makes preparations on program start
func Init() {
	InitDB()

	if err := checkMoviesPath(); err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
		return
	}
	if err := checkShowsPath(); err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
		return
	}

	go func() {
		// Give time to Kodi to start its JSON-RPC service
		time.Sleep(5 * time.Second)
		Refresh()
	}()

	// Removed episodes debouncer
	go func() {
		var episodes []*removedEpisode
		for {
			select {
			case <-time.After(3 * time.Second):
				if len(episodes) == 0 {
					break
				}

				shows := make(map[string][]*removedEpisode, 0)
				for _, episode := range episodes {
					shows[episode.ShowName] = append(shows[episode.ShowName], episode)
				}

				var label string
				var labels []string
				if len(episodes) > 1 {
					for showName, showEpisodes := range shows {
						var libraryTotal int
						if l.Shows == nil {
							break
						}
						for _, libraryShow := range l.Shows {
							if libraryShow.Xbmc.IMDBNumber == showEpisodes[0].ScraperID {
								log.Warningf("Library removed %d episodes for %s", libraryShow.Episodes, libraryShow.Title)
								libraryTotal = libraryShow.Xbmc.Episodes
								break
							}
						}
						if libraryTotal == 0 {
							break
						}
						if len(showEpisodes) == libraryTotal {
							ID := strconv.Itoa(showEpisodes[0].ShowID)
							if _, err := RemoveShow(ID); err != nil {
								log.Error("Unable to remove show after removing all episodes...")
							}
						} else {
							labels = append(labels, fmt.Sprintf("%d episodes of %s", len(showEpisodes), showName))
						}

						// Add single episodes to removed prefix
						var tmdbIDs []int
						for _, showEpisode := range showEpisodes {
							tmdbIDs = append(tmdbIDs, showEpisode.ID)
						}
						if err := updateDB(Batch, RemovedEpisodeType, tmdbIDs, showEpisodes[0].ShowID); err != nil {
							log.Error(err)
						}
					}
					if len(labels) > 0 {
						label = strings.Join(labels, ", ")
						if xbmc.DialogConfirm("Elementum", fmt.Sprintf("LOCALIZE[30278];;%s", label)) {
							xbmc.VideoLibraryClean()
						}
					}
				} else {
					for showName, episode := range shows {
						label = fmt.Sprintf("%s S%02dE%02d", showName, episode[0].Season, episode[0].Episode)
						// ID, _ := strconv.Atoi(episode[0].ShowID)
						if err := updateDB(Update, RemovedEpisodeType, []int{episode[0].ID}, episode[0].ShowID); err != nil {
							log.Error(err)
						}
					}
					if xbmc.DialogConfirm("Elementum", fmt.Sprintf("LOCALIZE[30278];;%s", label)) {
						xbmc.VideoLibraryClean()
					}
				}

				episodes = make([]*removedEpisode, 0)

			case episode, ok := <-removedEpisodes:
				if !ok {
					break
				}
				episodes = append(episodes, episode)
			}
		}
	}()

	updateDelay := config.Get().UpdateDelay
	if updateDelay > 0 {
		if updateDelay < 10 {
			// Give time to Elementum to update its cache of libraryMovies, libraryShows and libraryEpisodes
			updateDelay = 10
		}
		go func() {
			time.Sleep(time.Duration(updateDelay) * time.Second)
			select {
			case <-closing:
				return
			default:
				if err := doUpdateLibrary(); err != nil {
					log.Warning(err)
				}
				if config.Get().UpdateAutoScan && Scanning == false {
					Scanning = true
					xbmc.VideoLibraryScan()
					Scanning = false
				}
			}
		}()
	}

	log.Notice("Warming up caches...")
	tmdb.WarmingUp = true
	go func() {
		time.Sleep(30 * time.Second)
		if tmdb.WarmingUp == true {
			xbmc.Notify("Elementum", "LOCALIZE[30147]", config.AddonIcon())
		}
	}()
	started := time.Now()
	language := config.Get().Language
	tmdb.PopularMovies("", language, 1)
	tmdb.PopularShows("", language, 1)
	if _, _, err := trakt.TopMovies("trending", "1"); err != nil {
		log.Warning(err)
	}
	if _, _, err := trakt.TopShows("trending", "1"); err != nil {
		log.Warning(err)
	}
	tmdb.WarmingUp = false
	took := time.Since(started)
	if took.Seconds() > 30 {
		xbmc.Notify("Elementum", "LOCALIZE[30148]", config.AddonIcon())
	}
	log.Noticef("Caches warmed up in %s", took)

	updateFrequency := 1
	configUpdateFrequency := config.Get().UpdateFrequency
	if configUpdateFrequency > 1 {
		updateFrequency = configUpdateFrequency
	}
	updateTicker := time.NewTicker(time.Duration(updateFrequency) * time.Hour)
	defer updateTicker.Stop()

	traktFrequency := 1
	configTraktSyncFrequency := config.Get().TraktSyncFrequency
	if configTraktSyncFrequency > 1 {
		traktFrequency = configTraktSyncFrequency
	}
	traktSyncTicker := time.NewTicker(time.Duration(traktFrequency) * time.Hour)
	defer traktSyncTicker.Stop()

	markedForRemovalTicker := time.NewTicker(30 * time.Second)
	defer markedForRemovalTicker.Stop()

	for {
		select {
		case <-updateTicker.C:
			if config.Get().UpdateFrequency > 0 {
				if err := doUpdateLibrary(); err != nil {
					log.Warning(err)
				}
				if config.Get().UpdateAutoScan && Scanning == false && updateFrequency != traktFrequency {
					Scanning = true
					xbmc.VideoLibraryScan()
					Scanning = false
				}
			}
		case <-traktSyncTicker.C:
			if config.Get().TraktSyncFrequency > 0 {
				if err := RefreshTrakt(); err != nil {
					log.Warning(err)
				}
				if config.Get().UpdateAutoScan && Scanning == false {
					Scanning = true
					xbmc.VideoLibraryScan()
					Scanning = false
				}
			}
		case <-markedForRemovalTicker.C:
			db.ForEach(database.BitTorrentBucket, func(key []byte, value []byte) error {
				item := &database.BTItem{}
				if err := json.Unmarshal(value, &item); err != nil {
					log.Error(err)
					return err
				}
				if item.State > 0 {
					return nil
				}

				// Remove from Elementum's library to prevent duplicates
				if item.Type == movieType {
					if _, err := IsDuplicateMovie(strconv.Itoa(item.ID)); err != nil {
						RemoveMovie(item.ID)
						if _, err := RemoveMovie(item.ID); err != nil {
							log.Warning("Nothing left to remove from Elementum")
						}
					}
				} else {
					if episode, err := IsDuplicateEpisode(item.ShowID, item.Season, item.Episode); err != nil {
						if err := RemoveEpisode(item.ID, item.ShowID, strconv.Itoa(episode.ID), item.Season, item.Episode); err != nil {
							log.Warning(err)
						}
					}
				}
				ID, _ := strconv.Atoi(string(key))
				updateDB(DeleteTorrent, 0, []int{ID}, 0)
				log.Infof("Removed %s from database", key)
				return nil
			})
		case <-closing:
			close(removedEpisodes)
			return
		}
	}
}

// Refresh is updating library from Kodi
func Refresh() error {
	if TraktScanning {
		return nil
	}
	if err := RefreshMovies(); err != nil {
		log.Debugf("RefreshMovies got an error: %v", err)
	}
	if err := RefreshShows(); err != nil {
		log.Debugf("RefreshMovies got an error: %v", err)
	}

	log.Debug("Library refresh finished")
	return nil
}

// RefreshMovies updates movies in the library
func RefreshMovies() error {
	if Scanning {
		return nil
	}

	Scanning = true
	defer func() {
		Scanning = false
		RefreshUIDs()
	}()

	var movies *xbmc.VideoLibraryMovies
	for tries := 1; tries <= 3; tries++ {
		var err error
		movies, err = xbmc.VideoLibraryGetMovies()
		if movies == nil || err != nil {
			time.Sleep(time.Duration(tries*2) * time.Second)
			continue
		}
		break
	}

	if movies == nil || movies.Movies == nil {
		return errors.New("Could not fetch Movies from Kodi")
	}
	started := time.Now()
	defer func() {
		log.Debugf("Fetched %d movies from Kodi Library in %s", len(movies.Movies), time.Since(started))
	}()

	l.mu.Movies.Lock()
	defer l.mu.Movies.Unlock()

	l.Movies = map[int]*Movie{}
	for _, m := range movies.Movies {
		m.UniqueIDs.Kodi = m.ID
		if m.UniqueIDs.IMDB == "" && m.IMDBNumber != "" && strings.HasPrefix(m.IMDBNumber, "tt") {
			m.UniqueIDs.IMDB = m.IMDBNumber
		}

		l.Movies[m.ID] = &Movie{
			ID:      m.ID,
			Title:   m.Title,
			Watched: m.PlayCount > 0,
			File:    m.File,
			Resume:  &Resume{},
			UIDs:    &UniqueIDs{Kodi: m.ID},
			Xbmc:    m,
		}
	}

	for _, m := range l.Movies {
		parseUniqueID(MovieType, m.UIDs, &m.Xbmc.UniqueIDs, m.Xbmc.File, m.Xbmc.Year)
	}

	return nil
}

// RefreshShows updates shows in the library
func RefreshShows() error {
	if Scanning {
		return nil
	}

	Scanning = true
	defer func() {
		Scanning = false
		RefreshUIDs()
	}()

	var shows *xbmc.VideoLibraryShows
	for tries := 1; tries <= 3; tries++ {
		var err error
		shows, err = xbmc.VideoLibraryGetShows()
		if err != nil {
			time.Sleep(time.Duration(tries*2) * time.Second)
			continue
		}
		break
	}

	if shows == nil || shows.Shows == nil {
		return errors.New("Could not fetch Shows from Kodi")
	}
	started := time.Now()
	defer func() {
		log.Debugf("Fetched %d shows from Kodi Library in %s", len(shows.Shows), time.Since(started))
	}()

	l.mu.Shows.Lock()
	defer l.mu.Shows.Unlock()

	l.Shows = map[int]*Show{}
	for _, s := range shows.Shows {
		s.UniqueIDs.Kodi = s.ID
		if s.UniqueIDs.IMDB == "" && s.IMDBNumber != "" && strings.HasPrefix(s.IMDBNumber, "tt") {
			s.UniqueIDs.IMDB = s.IMDBNumber
		}

		l.Shows[s.ID] = &Show{
			ID:       s.ID,
			Title:    s.Title,
			Seasons:  map[int]*Season{},
			Episodes: map[int]*Episode{},
			UIDs:     &UniqueIDs{Kodi: s.ID},
			// UIDs:     parseUniqueID(ShowType, &s.UniqueIDs, ""),
			Xbmc: s,
		}
	}

	if err := RefreshSeasons(); err != nil {
		log.Debugf("RefreshSeasons got an error: %v", err)
	}
	if err := RefreshEpisodes(); err != nil {
		log.Debugf("RefreshEpisodes got an error: %v", err)
	}

	for _, show := range l.Shows {
		// Step 1: try to get information from what we get from Kodi
		parseUniqueID(ShowType, show.UIDs, &show.Xbmc.UniqueIDs, "", show.Xbmc.Year)

		// Step 2: if TMDB not found - try to find it from episodes
		if show.UIDs.TMDB == 0 {
			for _, e := range show.Episodes {
				if !strings.HasSuffix(e.Xbmc.File, ".strm") {
					continue
				}

				u := &UniqueIDs{}
				parseUniqueID(EpisodeType, u, &e.Xbmc.UniqueIDs, e.Xbmc.File, 0)
				if u.TMDB != 0 {
					show.UIDs.TMDB = u.TMDB
					break
				}
			}
		}

		if show.UIDs.TMDB == 0 {
			continue
		}

		for _, s := range show.Seasons {
			s.UIDs.TMDB = show.UIDs.TMDB
			s.UIDs.TVDB = show.UIDs.TVDB
			s.UIDs.IMDB = show.UIDs.IMDB
		}
		for _, e := range show.Episodes {
			e.UIDs.TMDB = show.UIDs.TMDB
			e.UIDs.TVDB = show.UIDs.TVDB
			e.UIDs.IMDB = show.UIDs.IMDB
		}
	}

	return nil
}

// RefreshSeasons updates seasons list for selected show in the library
func RefreshSeasons() error {
	var seasons *xbmc.VideoLibrarySeasons
	for tries := 1; tries <= 3; tries++ {
		var err error
		seasons, err = xbmc.VideoLibraryGetAllSeasons()
		if seasons == nil || err != nil {
			time.Sleep(time.Duration(tries*2) * time.Second)
			continue
		}
		break
	}

	if seasons == nil || seasons.Seasons == nil {
		return errors.New("Could not fetch Seasons from Kodi")
	}
	started := time.Now()
	defer func() {
		log.Debugf("Fetched %d seasons from Kodi Library in %s", len(seasons.Seasons), time.Since(started))
	}()

	cleanupCheck := map[int]bool{}
	for _, s := range seasons.Seasons {
		if c, ok := l.Shows[s.TVShowID]; !ok || c == nil || c.Seasons == nil {
			continue
		}

		if _, ok := cleanupCheck[s.TVShowID]; !ok {
			l.Shows[s.TVShowID].Seasons = map[int]*Season{}
			cleanupCheck[s.TVShowID] = true
		}

		s.UniqueIDs.Kodi = s.ID

		l.Shows[s.TVShowID].Seasons[s.ID] = &Season{
			ID:       s.ID,
			Title:    s.Title,
			Season:   s.Season,
			Episodes: s.Episodes,
			Watched:  s.PlayCount > 0,
			UIDs:     &UniqueIDs{Kodi: s.ID},
			Xbmc:     s,
		}
	}

	return nil
}

// RefreshEpisodes updates episodes list for selected show in the library
func RefreshEpisodes() error {
	var episodes *xbmc.VideoLibraryEpisodes
	for tries := 1; tries <= 3; tries++ {
		var err error
		episodes, err = xbmc.VideoLibraryGetAllEpisodes()
		if episodes == nil || err != nil {
			time.Sleep(time.Duration(tries*2) * time.Second)
			continue
		}
		break
	}

	if episodes == nil || episodes.Episodes == nil {
		return errors.New("Could not fetch Episodes from Kodi")
	}
	started := time.Now()
	defer func() {
		log.Debugf("Fetched %d episodes from Kodi Library in %s", len(episodes.Episodes), time.Since(started))
	}()

	cleanupCheck := map[int]bool{}
	for _, e := range episodes.Episodes {
		if c, ok := l.Shows[e.TVShowID]; !ok || c == nil || c.Episodes == nil {
			continue
		}

		if _, ok := cleanupCheck[e.TVShowID]; !ok {
			l.Shows[e.TVShowID].Episodes = map[int]*Episode{}
			cleanupCheck[e.TVShowID] = true
		}

		e.UniqueIDs.Kodi = e.ID
		e.UniqueIDs.TMDB = ""
		e.UniqueIDs.TVDB = ""
		e.UniqueIDs.Trakt = ""
		e.UniqueIDs.Unknown = ""

		l.Shows[e.TVShowID].Episodes[e.ID] = &Episode{
			ID:      e.ID,
			Title:   e.Title,
			Season:  e.Season,
			Episode: e.Episode,
			Watched: e.PlayCount > 0,
			Resume:  &Resume{},
			UIDs:    &UniqueIDs{Kodi: e.ID},
			Xbmc:    e,
		}
	}

	return nil
}

// RefreshUIDs updates unique IDs for each library item
// This collects already saved UIDs for easier access
func RefreshUIDs() error {
	l.mu.UIDs.Lock()
	defer l.mu.UIDs.Unlock()

	playcount.Mu.Lock()
	defer playcount.Mu.Unlock()
	playcount.Watched = map[uint64]bool{}
	l.UIDs = map[int]*UniqueIDs{}
	for k, v := range l.WatchedTrakt {
		playcount.Watched[k] = v
	}

	for _, m := range l.Movies {
		m.UIDs.MediaType = MovieType
		l.UIDs[m.ID] = m.UIDs

		if m.Watched {
			playcount.Watched[xxhash.Sum64String(fmt.Sprintf("%d_%d_%d", MovieType, TMDBScraper, m.UIDs.TMDB))] = true
			playcount.Watched[xxhash.Sum64String(fmt.Sprintf("%d_%d_%d", MovieType, TraktScraper, m.UIDs.Trakt))] = true
			playcount.Watched[xxhash.Sum64String(fmt.Sprintf("%d_%d_%s", MovieType, IMDBScraper, m.UIDs.IMDB))] = true
		}
	}
	for _, s := range l.Shows {
		s.UIDs.MediaType = ShowType
		l.UIDs[s.ID] = s.UIDs

		if s.Watched {
			playcount.Watched[xxhash.Sum64String(fmt.Sprintf("%d_%d_%d", ShowType, TMDBScraper, s.UIDs.TMDB))] = true
			playcount.Watched[xxhash.Sum64String(fmt.Sprintf("%d_%d_%d", ShowType, TraktScraper, s.UIDs.Trakt))] = true
			playcount.Watched[xxhash.Sum64String(fmt.Sprintf("%d_%d_%d", ShowType, TVDBScraper, s.UIDs.TVDB))] = true
		}

		for _, e := range l.Shows[s.ID].Seasons {
			e.UIDs.MediaType = SeasonType
			l.UIDs[e.ID] = e.UIDs

			if e.Watched {
				playcount.Watched[xxhash.Sum64String(fmt.Sprintf("%d_%d_%d_%d", SeasonType, TMDBScraper, s.UIDs.TMDB, e.Season))] = true
				playcount.Watched[xxhash.Sum64String(fmt.Sprintf("%d_%d_%d_%d", SeasonType, TraktScraper, s.UIDs.Trakt, e.Season))] = true
				playcount.Watched[xxhash.Sum64String(fmt.Sprintf("%d_%d_%d_%d", SeasonType, TVDBScraper, s.UIDs.TVDB, e.Season))] = true
			}
		}
		for _, e := range l.Shows[s.ID].Episodes {
			e.UIDs.MediaType = EpisodeType
			l.UIDs[e.ID] = e.UIDs

			if e.Watched {
				playcount.Watched[xxhash.Sum64String(fmt.Sprintf("%d_%d_%d_%d_%d", EpisodeType, TMDBScraper, s.UIDs.TMDB, e.Season, e.Episode))] = true
				playcount.Watched[xxhash.Sum64String(fmt.Sprintf("%d_%d_%d_%d_%d", EpisodeType, TraktScraper, s.UIDs.Trakt, e.Season, e.Episode))] = true
				playcount.Watched[xxhash.Sum64String(fmt.Sprintf("%d_%d_%d_%d_%d", EpisodeType, TVDBScraper, s.UIDs.TVDB, e.Season, e.Episode))] = true
			}
		}
	}

	log.Debugf("UIDs refresh finished")
	return nil
}

func parseUniqueID(entityType int, i *UniqueIDs, xbmcIDs *xbmc.UniqueIDs, fileName string, year int) {
	i.MediaType = entityType
	i.Kodi = xbmcIDs.Kodi
	i.IMDB = xbmcIDs.IMDB
	i.TMDB, _ = strconv.Atoi(xbmcIDs.TMDB)
	i.TVDB, _ = strconv.Atoi(xbmcIDs.TVDB)
	i.Trakt, _ = strconv.Atoi(xbmcIDs.Trakt)

	// Checking alternative fields
	// 		TheMovieDB
	if i.TMDB == 0 && len(xbmcIDs.TheMovieDB) > 0 {
		i.TMDB, _ = strconv.Atoi(xbmcIDs.TheMovieDB)
	}

	// 		IMDB
	if len(xbmcIDs.Unknown) > 0 && strings.HasPrefix(xbmcIDs.Unknown, "tt") {
		i.IMDB = xbmcIDs.Unknown
	}
	if i.TMDB != 0 {
		return
	}

	// If this is a strm file then we try to get TMDB id from it
	if len(fileName) > 0 {
		id, err := findTMDBInFile(fileName, xbmcIDs.Unknown)
		if id != 0 {
			i.TMDB = id
			return
		} else if err != nil {
			log.Debugf("Error reading TMDB ID from the file %s: %#v", fileName, err)
		}
	}

	// We should not query for each episode, has no sense,
	// since we need only TVShow ID to be resolved
	if entityType == EpisodeType {
		return
	}

	// If we get here - we have no TMDB, so try to resolve it
	if len(i.IMDB) != 0 {
		i.TMDB = findTMDBIDs(entityType, "imdb_id", i.IMDB)
		if i.TMDB != 0 {
			return
		}
	}
	if i.TVDB != 0 {
		i.TMDB = findTMDBIDs(entityType, "tvdb_id", strconv.Itoa(i.TVDB))
		if i.TMDB != 0 {
			return
		}
	}

	// We don't have any Named IDs, only 'Unknown' so let's try to fetch it
	if xbmcIDs.Unknown != "" {
		localID, _ := strconv.Atoi(xbmcIDs.Unknown)
		if localID == 0 {
			return
		}

		// Try to treat as it is a TMDB id inside of Unknown field
		if entityType == MovieType {
			m := tmdb.GetMovie(localID, config.Get().Language)
			if m != nil {
				dt, err := time.Parse("2006-01-02", m.FirstAirDate)
				if err != nil || dt.Year() == year {
					i.TMDB = m.ID
					return
				}
			}
		} else if entityType == ShowType {
			s := tmdb.GetShow(localID, config.Get().Language)
			if s != nil {
				dt, err := time.Parse("2006-01-02", s.FirstAirDate)
				if err != nil || dt.Year() == year {
					i.TMDB = s.ID
					return
				}
			}

			// If not found, try to search as TVDB id
			id := findTMDBIDsWithYear(ShowType, "tvdb_id", xbmcIDs.Unknown, year)
			if id != 0 {
				i.TMDB = id
				return
			}
		}
	}

	return
}

func findTMDBInFile(fileName string, pattern string) (id int, err error) {
	if len(fileName) == 0 || !strings.HasSuffix(fileName, ".strm") {
		return
	}

	// cacheKey := fmt.Sprintf("Resolve_File_%s", fileName)
	// if err := cacheStore.Get(cacheKey, id); err == nil {
	// 	return id
	// }
	defer func() {
		if id == 0 {
			log.Debugf("Count not get ID from the file %s with pattern %s", fileName, pattern)
		}
		// cacheStore.Set(cacheKey, id, resolveFileExpiration)
	}()

	if _, errStat := os.Stat(fileName); errStat != nil {
		return 0, errStat
	}

	fileContent, errRead := ioutil.ReadFile(fileName)
	// log.Debugf("C: %s === %s === %#v", fileName, fileContent, err)
	if errRead != nil {
		return 0, errRead
	}

	// Dummy check. If Unknown is found in the strm file - we treat it as tmdb id
	if len(pattern) > 1 && bytes.Contains(fileContent, []byte("/"+pattern)) {
		id, _ = strconv.Atoi(pattern)
		return
	}

	// Reading the strm file and passing to a regexp to get TMDB ID
	// This can't be done with episodes, since it has Show ID and not Episode ID
	if matches := resolveRegexp.FindSubmatch(fileContent); len(matches) > 1 {
		id, _ = strconv.Atoi(string(matches[1]))
		return
	}

	return
}

func findTMDBIDsWithYear(entityType int, source string, id string, year int) int {
	results := tmdb.Find(id, source)
	reserveID := 0

	if results != nil {
		if entityType == MovieType && len(results.MovieResults) > 0 {
			for _, e := range results.MovieResults {
				dt, err := time.Parse("2006-01-02", e.FirstAirDate)
				if err != nil || year == 0 || dt.Year() == 0 {
					reserveID = e.ID
					continue
				}
				if dt.Year() == year {
					return e.ID
				}
			}
		} else if entityType == ShowType && len(results.TVResults) > 0 {
			for _, e := range results.TVResults {
				dt, err := time.Parse("2006-01-02", e.FirstAirDate)
				if err != nil || year == 0 || dt.Year() == 0 {
					reserveID = e.ID
					continue
				}
				if dt.Year() == year {
					return e.ID
				}
			}
		} else if entityType == EpisodeType && len(results.TVEpisodeResults) > 0 {
			for _, e := range results.TVEpisodeResults {
				dt, err := time.Parse("2006-01-02", e.FirstAirDate)
				if err != nil || year == 0 || dt.Year() == 0 {
					reserveID = e.ID
					continue
				}
				if dt.Year() == year {
					return e.ID
				}
			}
		}
	}

	return reserveID
}

func findTMDBIDs(entityType int, source string, id string) int {
	results := tmdb.Find(id, source)
	if results != nil {
		if entityType == MovieType && len(results.MovieResults) == 1 && results.MovieResults[0] != nil {
			return results.MovieResults[0].ID
		} else if entityType == ShowType && len(results.TVResults) == 1 && results.TVResults[0] != nil {
			return results.TVResults[0].ID
		} else if entityType == EpisodeType && len(results.TVEpisodeResults) == 1 && results.TVEpisodeResults[0] != nil {
			return results.TVEpisodeResults[0].ID
		}
	}

	return 0
}

func findTraktIDs(entityType int, source int, id string) (ids *trakt.IDs) {
	switch entityType {
	case MovieType:
		var r *trakt.Movie
		if source == TMDBScraper {
			r = trakt.GetMovieByTMDB(id)
		} else if source == TraktScraper {
			r = trakt.GetMovie(id)
		}
		if r != nil && r.IDs != nil {
			ids = r.IDs
		}
	case ShowType:
		var r *trakt.Show
		if source == TMDBScraper {
			r = trakt.GetShowByTMDB(id)
		} else if source == TraktScraper {
			r = trakt.GetShow(id)
		}
		if r != nil && r.IDs != nil {
			ids = r.IDs
		}
	case EpisodeType:
		var r *trakt.Episode
		if source == TMDBScraper {
			r = trakt.GetEpisodeByTMDB(id)
		} else if source == TraktScraper {
			r = trakt.GetEpisode(id)
		}
		if r != nil && r.IDs != nil {
			ids = r.IDs
		}
	}

	return
}

//
// Library updates
//
func doUpdateLibrary() error {
	if err := checkShowsPath(); err != nil {
		return err
	}

	db.Seek(dbBucket, fmt.Sprintf("%d_", ShowType), func(key []byte, value []byte) {
		item := &DBItem{}
		if err := json.Unmarshal(value, &item); err != nil {
			return
		}
		if _, err := writeShowStrm(item.ID, false); err != nil {
			log.Error(err)
		}
	})

	log.Notice("Library updated")
	return nil
}

//
// Path checks
//
func checkLibraryPath() error {
	if libraryPath == "" {
		libraryPath = config.Get().LibraryPath
	}
	if libraryPath == "" || libraryPath == "." {
		return errors.New("LOCALIZE[30220]")
	}
	if fileInfo, err := os.Stat(libraryPath); err != nil {
		if fileInfo == nil {
			return errors.New("Invalid library path")
		}
		if !fileInfo.IsDir() {
			return errors.New("Library path is not a directory")
		}
		return err
	}
	return nil
}

func checkMoviesPath() error {
	if err := checkLibraryPath(); err != nil {
		return err
	}
	if moviesLibraryPath == "" {
		moviesLibraryPath = filepath.Join(libraryPath, "Movies")
	}
	if _, err := os.Stat(moviesLibraryPath); os.IsNotExist(err) {
		if err := os.Mkdir(moviesLibraryPath, 0755); err != nil {
			log.Error(err)
			return err
		}
	}
	return nil
}

func checkShowsPath() error {
	if err := checkLibraryPath(); err != nil {
		return err
	}
	if showsLibraryPath == "" {
		showsLibraryPath = filepath.Join(libraryPath, "Shows")
	}
	if _, err := os.Stat(showsLibraryPath); os.IsNotExist(err) {
		if err := os.Mkdir(showsLibraryPath, 0755); err != nil {
			log.Error(err)
			return err
		}
	}
	return nil
}

//
// Writers
//

func writeMovieStrm(tmdbID string) (*tmdb.Movie, error) {
	movie := tmdb.GetMovieByID(tmdbID, config.Get().Language)
	if movie == nil {
		return nil, errors.New("Can't find the movie")
	}

	movieStrm := util.ToFileName(fmt.Sprintf("%s (%s)", movie.OriginalTitle, strings.Split(movie.ReleaseDate, "-")[0]))
	moviePath := filepath.Join(moviesLibraryPath, movieStrm)

	if _, err := os.Stat(moviePath); os.IsNotExist(err) {
		if err := os.Mkdir(moviePath, 0755); err != nil {
			log.Error(err)
			return movie, err
		}
	}

	movieStrmPath := filepath.Join(moviePath, fmt.Sprintf("%s.strm", movieStrm))

	playLink := URLForXBMC("/library/movie/play/%s", tmdbID)
	if _, err := os.Stat(movieStrmPath); err == nil {
		return movie, fmt.Errorf("LOCALIZE[30287];;%s", movie.Title)
	}
	if err := ioutil.WriteFile(movieStrmPath, []byte(playLink), 0644); err != nil {
		log.Error(err)
		return movie, err
	}

	return movie, nil
}

func writeShowStrm(showID string, adding bool) (*tmdb.Show, error) {
	ID, _ := strconv.Atoi(showID)
	show := tmdb.GetShow(ID, config.Get().Language)
	if show == nil {
		return nil, fmt.Errorf("Unable to get show (%s)", showID)
	}
	showStrm := util.ToFileName(fmt.Sprintf("%s (%s)", show.OriginalName, strings.Split(show.FirstAirDate, "-")[0]))
	showPath := filepath.Join(showsLibraryPath, showStrm)

	if _, err := os.Stat(showPath); os.IsNotExist(err) {
		if err := os.Mkdir(showPath, 0755); err != nil {
			log.Error(err)
			return show, err
		}
	}

	now := time.Now().UTC()
	addSpecials := config.Get().AddSpecials

	for _, season := range show.Seasons {
		if season.EpisodeCount == 0 {
			continue
		}
		if config.Get().ShowUnairedSeasons == false {
			firstAired, _ := time.Parse("2006-01-02", show.FirstAirDate)
			if firstAired.After(now) {
				continue
			}
		}
		if addSpecials == false && season.Season == 0 {
			continue
		}

		seasonTMDB := tmdb.GetSeason(ID, season.Season, config.Get().Language)
		if seasonTMDB == nil {
			continue
		}
		episodes := seasonTMDB.Episodes

		var reAddIDs []int
		for _, episode := range episodes {
			if episode == nil {
				continue
			}
			if config.Get().ShowUnairedEpisodes == false {
				if episode.AirDate == "" {
					continue
				}
				firstAired, _ := time.Parse("2006-01-02", episode.AirDate)
				if firstAired.After(now) {
					continue
				}
			}

			if adding {
				reAddIDs = append(reAddIDs, episode.ID)
			} else {
				// Check if single episode was previously removed
				if wasRemoved(strconv.Itoa(episode.ID), RemovedEpisodeType) {
					continue
				}
			}

			if _, err := IsDuplicateEpisode(ID, season.Season, episode.EpisodeNumber); err != nil {
				continue
			}

			episodeStrmPath := filepath.Join(showPath, fmt.Sprintf("%s S%02dE%02d.strm", showStrm, season.Season, episode.EpisodeNumber))
			playLink := URLForXBMC("/library/show/play/%d/%d/%d", ID, season.Season, episode.EpisodeNumber)
			if _, err := os.Stat(episodeStrmPath); err == nil {
				// log.Warningf("%s already exists, skipping", episodeStrmPath)
				continue
			}
			if err := ioutil.WriteFile(episodeStrmPath, []byte(playLink), 0644); err != nil {
				log.Error(err)
				return show, err
			}
		}
		if len(reAddIDs) > 0 {
			if err := updateDB(BatchDelete, RemovedEpisodeType, reAddIDs, ID); err != nil {
				log.Error(err)
			}
		}
	}

	return show, nil
}

//
// Removers
//

// RemoveMovie removes movie from the library
func RemoveMovie(tmdbID int) (*tmdb.Movie, error) {
	if err := checkMoviesPath(); err != nil {
		return nil, err
	}
	ID := strconv.Itoa(tmdbID)
	movie := tmdb.GetMovieByID(ID, config.Get().Language)
	if movie == nil {
		return nil, errors.New("Can't resolve movie")
	}
	movieName := fmt.Sprintf("%s (%s)", movie.OriginalTitle, strings.Split(movie.ReleaseDate, "-")[0])
	movieStrm := util.ToFileName(movieName)
	moviePath := filepath.Join(moviesLibraryPath, movieStrm)

	if _, err := os.Stat(moviePath); err != nil {
		return movie, errors.New("LOCALIZE[30282]")
	}
	if err := os.RemoveAll(moviePath); err != nil {
		return movie, err
	}

	if err := updateDB(Delete, MovieType, []int{tmdbID}, 0); err != nil {
		return movie, err
	}
	if err := updateDB(Update, RemovedMovieType, []int{tmdbID}, 0); err != nil {
		return movie, err
	}
	log.Warningf("%s removed from library", movieName)

	return movie, nil
}

// RemoveShow removes show from the library
func RemoveShow(tmdbID string) (*tmdb.Show, error) {
	if err := checkShowsPath(); err != nil {
		return nil, err
	}
	ID, _ := strconv.Atoi(tmdbID)
	show := tmdb.GetShow(ID, config.Get().Language)

	if show == nil {
		return nil, errors.New("Unable to find show to remove")
	}

	showStrm := util.ToFileName(fmt.Sprintf("%s (%s)", show.Name, strings.Split(show.FirstAirDate, "-")[0]))
	showPath := filepath.Join(showsLibraryPath, showStrm)

	if _, err := os.Stat(showPath); err != nil {
		log.Warning(err)
		return show, errors.New("LOCALIZE[30282]")
	}
	if err := os.RemoveAll(showPath); err != nil {
		log.Error(err)
		return show, err
	}

	if err := updateDB(Delete, ShowType, []int{ID}, 0); err != nil {
		return show, err
	}
	if err := updateDB(Update, RemovedShowType, []int{ID}, 0); err != nil {
		return show, err
	}
	log.Warningf("%s removed from library", show.Name)

	return show, nil
}

// RemoveEpisode removes episode from the library
func RemoveEpisode(tmdbID int, showID int, scraperID string, seasonNumber int, episodeNumber int) error {
	if err := checkShowsPath(); err != nil {
		return err
	}
	show := tmdb.GetShow(showID, config.Get().Language)

	if show == nil {
		return errors.New("Unable to find show to remove episode")
	}

	showPath := util.ToFileName(fmt.Sprintf("%s (%s)", show.Name, strings.Split(show.FirstAirDate, "-")[0]))
	episodeStrm := fmt.Sprintf("%s S%02dE%02d.strm", showPath, seasonNumber, episodeNumber)
	episodePath := filepath.Join(showsLibraryPath, showPath, episodeStrm)

	alreadyRemoved := false
	if _, err := os.Stat(episodePath); err != nil {
		alreadyRemoved = true
	}
	if !alreadyRemoved {
		if err := os.Remove(episodePath); err != nil {
			return err
		}
	}

	removedEpisodes <- &removedEpisode{
		ID:        tmdbID,
		ShowID:    showID,
		ScraperID: scraperID,
		ShowName:  show.Name,
		Season:    seasonNumber,
		Episode:   episodeNumber,
	}

	if !alreadyRemoved {
		log.Noticef("%s removed from library", episodeStrm)
	} else {
		return errors.New("Nothing left to remove from Elementum")
	}

	return nil
}

//
// Duplicate handling
//

// IsDuplicateMovie checks if movie exists in the library
func IsDuplicateMovie(tmdbID string) (*tmdb.Movie, error) {
	movie := tmdb.GetMovieByID(tmdbID, config.Get().Language)
	if movie == nil {
		return movie, nil
	}

	l.mu.UIDs.Lock()
	defer l.mu.UIDs.Unlock()

	query, _ := strconv.Atoi(tmdbID)
	for _, u := range l.UIDs {
		if u.TMDB != 0 && u.MediaType == MovieType && u.TMDB == query {
			return movie, fmt.Errorf("%s already in library", movie.Title)
		}
	}

	return movie, nil
}

// IsDuplicateShow checks if show exists in the library
func IsDuplicateShow(tmdbID string) (*tmdb.Show, error) {
	show := tmdb.GetShowByID(tmdbID, config.Get().Language)
	if show == nil {
		return nil, errors.New("Can't resolve show")
	}

	l.mu.UIDs.Lock()
	defer l.mu.UIDs.Unlock()

	query, _ := strconv.Atoi(tmdbID)
	for _, u := range l.UIDs {
		if u.TMDB != 0 && u.MediaType == ShowType && u.TMDB == query {
			return show, fmt.Errorf("%s already in library", show.Title)
		}
	}

	return show, nil
}

// IsDuplicateEpisode checks if episode exists in the library
func IsDuplicateEpisode(tmdbShowID int, seasonNumber int, episodeNumber int) (episode *tmdb.Episode, err error) {
	episode = tmdb.GetEpisode(tmdbShowID, seasonNumber, episodeNumber, config.Get().Language)
	noExternalIDs := fmt.Sprintf("No external IDs found for S%02dE%02d (%d)", seasonNumber, episodeNumber, tmdbShowID)
	if episode == nil || episode.ExternalIDs == nil {
		log.Warning(noExternalIDs + ". No ExternalIDs")
		return
	}

	l.mu.UIDs.Lock()
	defer l.mu.UIDs.Unlock()

	for _, u := range l.UIDs {
		if u.TMDB != 0 && u.MediaType == EpisodeType && u.TMDB == episode.ID {
			return episode, fmt.Errorf("%s S%02dE%02d already in library", episode.Name, seasonNumber, episodeNumber)
		}
	}

	// episodeID = strconv.Itoa(episode.ID)
	// switch config.Get().TvScraper {
	// case TMDBScraper:
	// 	break
	// case TVDBScraper:
	// 	if episode.ExternalIDs == nil || episode.ExternalIDs.TVDBID == nil {
	// 		log.Warningf(noExternalIDs + ". No ExternalIDs for TVDB")
	// 		return
	// 	}
	// 	episodeID = strconv.Itoa(util.StrInterfaceToInt(episode.ExternalIDs.TVDBID))
	// case TraktScraper:
	// 	traktEpisode := trakt.GetEpisodeByTMDB(episodeID)
	// 	if traktEpisode == nil || traktEpisode.IDs == nil || traktEpisode.IDs.Trakt == 0 {
	// 		log.Warning(noExternalIDs + " from Trakt episode")
	// 		return
	// 	}
	// 	episodeID = strconv.Itoa(traktEpisode.IDs.Trakt)
	// }
	//
	// var showID string
	// switch config.Get().TvScraper {
	// case TMDBScraper:
	// 	showID = strconv.Itoa(tmdbShowID)
	// case TVDBScraper:
	// 	show := tmdb.GetShowByID(strconv.Itoa(tmdbShowID), config.Get().Language)
	// 	if show == nil || show.ExternalIDs == nil || show.ExternalIDs.TVDBID == nil {
	// 		log.Warning(noExternalIDs + " for TVDB show")
	// 		return
	// 	}
	// 	showID = strconv.Itoa(util.StrInterfaceToInt(show.ExternalIDs.TVDBID))
	// case TraktScraper:
	// 	traktShow := trakt.GetShowByTMDB(strconv.Itoa(tmdbShowID))
	// 	if traktShow == nil || traktShow.IDs == nil || traktShow.IDs.Trakt == 0 {
	// 		log.Warning(noExternalIDs + " from Trakt show")
	// 		return
	// 	}
	// 	showID = strconv.Itoa(traktShow.IDs.Trakt)
	// }
	//
	// var tvshowID int
	// if libraryShows == nil {
	// 	return
	// }
	// for _, existingShow := range libraryShows.Shows {
	// 	if existingShow.ScraperID == showID {
	// 		tvshowID = existingShow.ID
	// 		break
	// 	}
	// }
	// if tvshowID == 0 {
	// 	return
	// }
	//
	// if libraryEpisodes == nil {
	// 	return
	// }
	// if episodes, exists := libraryEpisodes[tvshowID]; exists {
	// 	if episodes == nil {
	// 		return
	// 	}
	// 	for _, existingEpisode := range episodes.Episodes {
	// 		if existingEpisode.UniqueIDs.ID == episodeID ||
	// 			(existingEpisode.Season == seasonNumber && existingEpisode.Episode == episodeNumber) {
	// 			err = fmt.Errorf("%s S%02dE%02d already in library", existingEpisode.Title, seasonNumber, episodeNumber)
	// 			return
	// 		}
	// 	}
	// } else {
	// 	log.Warningf("Missing tvshowid (%d) in library episodes for S%02dE%02d (%s)", tvshowID, seasonNumber, episodeNumber, showID)
	// }
	return
}

// IsAddedToLibrary checks if specific TMDB exists in the library
func IsAddedToLibrary(id string, addedType int) (isAdded bool) {
	db.Seek(dbBucket, fmt.Sprintf("%d_", addedType), func(key []byte, value []byte) {
		itemID := strings.Split(string(key), "_")[1]
		if itemID == id {
			isAdded = true
			return
		}
	})

	return
}

//
// Database updates
//
func updateDB(Operation int, Type int, IDs []int, TVShowID int) error {
	switch Operation {
	case Update:
		item := DBItem{
			ID:       strconv.Itoa(IDs[0]),
			Type:     Type,
			TVShowID: TVShowID,
		}

		return db.SetObject(dbBucket, fmt.Sprintf("%d_%d", Type, IDs[0]), item)
	case Batch:
		objects := map[string]interface{}{}
		for _, id := range IDs {
			item := DBItem{
				ID:       strconv.Itoa(id),
				Type:     Type,
				TVShowID: TVShowID,
			}
			objects[fmt.Sprintf("%d_%d", Type, id)] = item
		}

		return db.BatchSetObject(dbBucket, objects)
	case Delete:
		return db.Delete(dbBucket, fmt.Sprintf("%d_%d", Type, IDs[0]))
	case BatchDelete:
		items := make([]string, len(IDs))
		for i, key := range IDs {
			items[i] = fmt.Sprintf("%d_%d", Type, key)
		}

		return db.BatchDelete(dbBucket, items)
	case DeleteTorrent:
		return db.Delete(database.BitTorrentBucket, strconv.Itoa(IDs[0]))
	}

	return nil
}

func wasRemoved(id string, removedType int) (wasRemoved bool) {
	if v, err := db.Get(dbBucket, fmt.Sprintf("%d_%s", removedType, id)); err == nil && len(v) > 0 {
		wasRemoved = true
	}

	return
}

//
// Maintenance
//

// CloseLibrary ...
func CloseLibrary() {
	log.Info("Closing library...")
	close(closing)
}

// ClearPageCache deletes cached page listings
func ClearPageCache() {
	cacheDB := database.GetCache()
	if cacheDB != nil {
		cacheDB.DeleteWithPrefix(database.CommonBucket, []byte("page."))
	}
	xbmc.Refresh()
}

// ClearResolveCache deletes cached IDs resolve
func ClearResolveCache() {
	cacheDB := database.GetCache()
	if cacheDB != nil {
		cacheDB.DeleteWithPrefix(database.CommonBucket, []byte("Resolve_"))
	}
}

// ClearCacheKey deletes specific key
func ClearCacheKey(key string) {
	cacheDB := database.GetCache()
	if cacheDB != nil {
		log.Debugf("Removing cache key: %s", key)
		if err := cacheDB.Delete(database.CommonBucket, key); err != nil {
			log.Debugf("Error removing key from cache: %#v", err)
		}
	}
}

// ClearTraktCache deletes cached trakt data
func ClearTraktCache() {
	cacheDB := database.GetCache()
	if cacheDB != nil {
		cacheDB.DeleteWithPrefix(database.CommonBucket, []byte("com.trakt."))
	}
	xbmc.Refresh()
}

// ClearTmdbCache deletes cached tmdb data
func ClearTmdbCache() {
	cacheDB := database.GetCache()
	if cacheDB != nil {
		cacheDB.DeleteWithPrefix(database.CommonBucket, []byte("com.tmdb."))
	}
	xbmc.Refresh()
}

//
// Utilities
// 		mainly copied from api/routes to skip cycle imports

// URLForHTTP ...
func URLForHTTP(pattern string, args ...interface{}) string {
	u, _ := url.Parse(fmt.Sprintf(pattern, args...))
	return util.GetHTTPHost() + u.String()
}

// URLForXBMC ...
func URLForXBMC(pattern string, args ...interface{}) string {
	u, _ := url.Parse(fmt.Sprintf(pattern, args...))
	return "plugin://" + config.Get().Info.ID + u.String()
}

// URLQuery ...
func URLQuery(route string, query ...string) string {
	v := url.Values{}
	for i := 0; i < len(query); i += 2 {
		v.Add(query[i], query[i+1])
	}
	return route + "?" + v.Encode()
}

//
// Trakt syncs
//

// RefreshTrakt starts a trakt sync
func RefreshTrakt() error {
	if Scanning {
		return nil
	}

	Scanning = true
	defer func() {
		Scanning = false
	}()

	if err := checkMoviesPath(); err != nil {
		return err
	}
	if err := checkShowsPath(); err != nil {
		return err
	}

	log.Debugf("TraktSync: Watched")
	if changes, err := SyncTraktWatched(); err != nil {
		return err
	} else if changes {
		Refresh()
		xbmc.Refresh()
	}
	if config.Get().TraktSyncWatchlist {
		log.Debugf("TraktSync: Movies Watchlist")
		if err := SyncMoviesList("watchlist", true); err != nil {
			return err
		}
		log.Debugf("TraktSync: Shows Watchlist")
		if err := SyncShowsList("watchlist", true); err != nil {
			return err
		}
	}
	if config.Get().TraktSyncCollections {
		log.Debugf("TraktSync: Movies Collections")
		if err := SyncMoviesList("collection", true); err != nil {
			return err
		}
		log.Debugf("TraktSync: Shows Collections")
		if err := SyncShowsList("collection", true); err != nil {
			return err
		}
	}

	if config.Get().TraktSyncUserlists {
		log.Debugf("TraktSync: Userlists")
		lists := trakt.Userlists()
		for _, list := range lists {
			if err := SyncMoviesList(strconv.Itoa(list.IDs.Trakt), true); err != nil {
				continue
			}
			if err := SyncShowsList(strconv.Itoa(list.IDs.Trakt), true); err != nil {
				continue
			}
		}
	}

	log.Debugf("TraktSync: Finished")
	return nil
}

// SyncTraktWatched gets watched list and updates watched status in the library
func SyncTraktWatched() (haveChanges bool, err error) {
	if config.Get().TraktToken == "" || !config.Get().TraktSyncWatched {
		return
	}

	started := time.Now()
	TraktScanning = true
	defer func() {
		log.Debugf("Trakt sync watched finished in %s", time.Since(started))
		TraktScanning = false
		RefreshUIDs()
	}()

	movies, errMovies := trakt.WatchedMovies()
	if errMovies != nil {
		return false, errMovies
	}

	l.mu.Trakt.Lock()

	l.WatchedTrakt = map[uint64]bool{}
	watchedMovies := map[int]bool{}
	for _, m := range movies {
		l.WatchedTrakt[xxhash.Sum64String(fmt.Sprintf("%d_%d_%d", MovieType, TraktScraper, m.Movie.IDs.Trakt))] = true
		l.WatchedTrakt[xxhash.Sum64String(fmt.Sprintf("%d_%d_%d", MovieType, TMDBScraper, m.Movie.IDs.TMDB))] = true
		l.WatchedTrakt[xxhash.Sum64String(fmt.Sprintf("%d_%d_%s", MovieType, IMDBScraper, m.Movie.IDs.IMDB))] = true

		var r *Movie
		if r == nil && m.Movie.IDs.TMDB != 0 {
			r, _ = GetMovieByTMDB(m.Movie.IDs.TMDB)
		}
		if r == nil && m.Movie.IDs.IMDB != "" {
			r, _ = GetMovieByIMDB(m.Movie.IDs.IMDB)
		}

		if r == nil {
			continue
		} else if r != nil {
			watchedMovies[r.UIDs.TMDB] = true

			if !r.Watched {
				haveChanges = true
				xbmc.SetMovieWatched(r.UIDs.Kodi, 1, 0, 0)
			}
		}
	}
	l.mu.Trakt.Unlock()

	shows, errShows := trakt.WatchedShows()
	if errShows != nil {
		return false, errShows
	}

	l.mu.Trakt.Lock()

	watchedShows := map[int]bool{}
	for _, s := range shows {
		tmdbShow := tmdb.GetShowByID(strconv.Itoa(s.Show.IDs.TMDB), config.Get().Language)
		completedSeasons := 0
		for _, season := range s.Seasons {
			if tmdbShow != nil {
				tmdbSeason := tmdb.GetSeason(s.Show.IDs.TMDB, season.Number, config.Get().Language)
				if tmdbSeason != nil && tmdbSeason.EpisodeCount == len(season.Episodes) {
					completedSeasons++

					l.WatchedTrakt[xxhash.Sum64String(fmt.Sprintf("%d_%d_%d_%d", SeasonType, TMDBScraper, s.Show.IDs.TMDB, season.Number))] = true
					l.WatchedTrakt[xxhash.Sum64String(fmt.Sprintf("%d_%d_%d_%d", SeasonType, TraktScraper, s.Show.IDs.Trakt, season.Number))] = true
				}
			}

			for _, episode := range season.Episodes {
				l.WatchedTrakt[xxhash.Sum64String(fmt.Sprintf("%d_%d_%d_%d_%d", EpisodeType, TMDBScraper, s.Show.IDs.TMDB, season.Number, episode.Number))] = true
				l.WatchedTrakt[xxhash.Sum64String(fmt.Sprintf("%d_%d_%d_%d_%d", EpisodeType, TraktScraper, s.Show.IDs.Trakt, season.Number, episode.Number))] = true
			}
		}

		if tmdbShow != nil && completedSeasons == len(tmdbShow.Seasons) {
			s.Watched = true
			l.WatchedTrakt[xxhash.Sum64String(fmt.Sprintf("%d_%d_%d", ShowType, TMDBScraper, s.Show.IDs.TMDB))] = true
			l.WatchedTrakt[xxhash.Sum64String(fmt.Sprintf("%d_%d_%d", ShowType, TraktScraper, s.Show.IDs.Trakt))] = true
		}

		var r *Show
		if r == nil && s.Show.IDs.TMDB != 0 {
			r, _ = GetShowByTMDB(s.Show.IDs.TMDB)
		}
		if r == nil && s.Show.IDs.IMDB != "" {
			r, _ = GetShowByIMDB(s.Show.IDs.IMDB)
		}

		if r == nil {
			continue
		} else if r != nil {
			if s.Watched {
				watchedShows[r.UIDs.Kodi] = true
				xbmc.SetShowWatched(r.UIDs.Kodi, 1)
			}

			for _, season := range s.Seasons {
				for _, episode := range season.Episodes {
					e := r.GetEpisode(season.Number, episode.Number)
					if e != nil {
						watchedShows[e.UIDs.Kodi] = true

						if !e.Watched {
							haveChanges = true
							xbmc.SetEpisodeWatched(e.UIDs.Kodi, 1, 0, 0)
						}
					}
				}
			}
		}
	}
	l.mu.Trakt.Unlock()

	if !config.Get().TraktSyncWatchedBack {
		return
	}

	// Now, when we know what is marked Watched on Trakt - we are
	// looking at Kodi library and sync back to Trakt items,
	// watched in Kodi and not marked on Trakt
	syncMovies := []*trakt.WatchedItem{}
	syncShows := []*trakt.WatchedItem{}

	l.mu.Movies.Lock()
	for _, m := range l.Movies {
		if m.UIDs.TMDB == 0 {
			continue
		}
		cacheKey := fmt.Sprintf("Synced_%d_%d", MovieType, m.UIDs.TMDB)
		if _, ok := watchedMovies[m.UIDs.TMDB]; ok || !m.Watched || db.Has(dbBucket, cacheKey) {
			continue
		}
		db.Set(dbBucket, cacheKey, "1")

		syncMovies = append(syncMovies, &trakt.WatchedItem{
			MediaType: "movie",
			Movie:     m.UIDs.TMDB,
			Watched:   true,
		})
	}
	l.mu.Movies.Unlock()

	l.mu.Shows.Lock()
	for _, s := range l.Shows {
		if s.UIDs.TMDB == 0 {
			continue
		}
		if _, ok := watchedShows[s.UIDs.Kodi]; ok {
			continue
		}

		for _, e := range s.Episodes {
			if _, ok := watchedShows[e.UIDs.Kodi]; ok || !e.Watched {
				continue
			}

			cacheKey := fmt.Sprintf("Synced_%d_%d", EpisodeType, e.UIDs.Kodi)
			if db.Has(dbBucket, cacheKey) {
				continue
			}
			db.Set(dbBucket, cacheKey, "1")

			syncShows = append(syncShows, &trakt.WatchedItem{
				MediaType: "episode",
				Show:      s.UIDs.TMDB,
				Season:    e.Season,
				Episode:   e.Episode,
				Watched:   true,
			})
		}
	}
	l.mu.Shows.Unlock()

	if len(syncMovies) > 0 {
		trakt.SetMultipleWatched(syncMovies)
	}
	if len(syncShows) > 0 {
		trakt.SetMultipleWatched(syncShows)
	}

	return
}

//
// Movie internals
//

// SyncMoviesList updates trakt movie collections in cache
func SyncMoviesList(listID string, updating bool) (err error) {
	if err = checkMoviesPath(); err != nil {
		return
	}

	var label string
	var movies []*trakt.Movies

	switch listID {
	case "watchlist":
		movies, err = trakt.WatchlistMovies()
		label = "LOCALIZE[30254]"
	case "collection":
		movies, err = trakt.CollectionMovies()
		label = "LOCALIZE[30257]"
	default:
		movies, err = trakt.ListItemsMovies(listID, false)
		label = "LOCALIZE[30263]"
	}

	if err != nil {
		log.Error(err)
		return
	}

	var movieIDs []int
	for _, movie := range movies {
		title := movie.Movie.Title
		// Try to resolve TMDB id through IMDB id, if provided
		if movie.Movie.IDs.TMDB == 0 && len(movie.Movie.IDs.IMDB) > 0 {
			r := tmdb.Find(movie.Movie.IDs.IMDB, "imdb_id")
			if r != nil && len(r.MovieResults) > 0 {
				movie.Movie.IDs.TMDB = r.MovieResults[0].ID
			}
		}

		if movie.Movie.IDs.TMDB == 0 {
			log.Warningf("Missing TMDB ID for %s", title)
			continue
		}

		tmdbID := strconv.Itoa(movie.Movie.IDs.TMDB)

		if updating && wasRemoved(tmdbID, RemovedMovieType) {
			continue
		}

		if _, err := IsDuplicateMovie(tmdbID); err != nil {
			continue
		}

		if _, err := writeMovieStrm(tmdbID); err != nil {
			continue
		}

		movieIDs = append(movieIDs, movie.Movie.IDs.TMDB)
	}

	if err := updateDB(Batch, MovieType, movieIDs, 0); err != nil {
		return err
	}

	if !updating {
		log.Noticef("Movies list (%s) added", listID)
		if xbmc.DialogConfirm("Elementum", fmt.Sprintf("LOCALIZE[30277];;%s", label)) {
			xbmc.VideoLibraryScan()
		}
	}
	return nil
}

//
// Shows internals
//

// SyncShowsList updates trakt collections in cache
func SyncShowsList(listID string, updating bool) (err error) {
	if err = checkShowsPath(); err != nil {
		return err
	}

	var label string
	var shows []*trakt.Shows

	switch listID {
	case "watchlist":
		shows, err = trakt.WatchlistShows()
		label = "LOCALIZE[30254]"
	case "collection":
		shows, err = trakt.CollectionShows()
		label = "LOCALIZE[30257]"
	default:
		shows, err = trakt.ListItemsShows(listID, false)
		label = "LOCALIZE[30263]"
	}

	if err != nil {
		log.Error(err)
		return
	}

	var showIDs []int
	for _, show := range shows {
		title := show.Show.Title
		// Try to resolve TMDB id through IMDB id, if provided
		if show.Show.IDs.TMDB == 0 {
			if len(show.Show.IDs.IMDB) > 0 {
				r := tmdb.Find(show.Show.IDs.IMDB, "imdb_id")
				if r != nil && len(r.TVResults) > 0 {
					show.Show.IDs.TMDB = r.TVResults[0].ID
				}
			}
			if show.Show.IDs.TMDB == 0 && show.Show.IDs.TVDB != 0 {
				r := tmdb.Find(strconv.Itoa(show.Show.IDs.TVDB), "tvdb_id")
				if r != nil && len(r.TVResults) > 0 {
					show.Show.IDs.TMDB = r.TVResults[0].ID
				}
			}
		}

		if show.Show.IDs.TMDB == 0 {
			log.Warningf("Missing TMDB ID for %s", title)
			continue
		}

		tmdbID := strconv.Itoa(show.Show.IDs.TMDB)

		if updating && wasRemoved(tmdbID, RemovedShowType) {
			continue
		}

		if !updating {
			if _, err := IsDuplicateShow(tmdbID); err != nil {
				continue
			}
		}

		if _, err := writeShowStrm(tmdbID, false); err != nil {
			continue
		}

		ID, _ := strconv.Atoi(tmdbID)
		showIDs = append(showIDs, ID)
	}

	if err := updateDB(Batch, ShowType, showIDs, 0); err != nil {
		return err
	}

	if !updating {
		log.Noticef("Shows list (%s) added", listID)
		if xbmc.DialogConfirm("Elementum", fmt.Sprintf("LOCALIZE[30277];;%s", label)) {
			xbmc.VideoLibraryScan()
		}
	}
	return nil
}

//
// External handlers
//

// AddMovie is adding movie to the library
func AddMovie(tmdbID string) (*tmdb.Movie, error) {
	if err := checkMoviesPath(); err != nil {
		return nil, err
	}

	ID, _ := strconv.Atoi(tmdbID)
	movie, errGet := IsDuplicateMovie(tmdbID)
	if errGet != nil {
		log.Warningf(errGet.Error())
		xbmc.Notify("Elementum", fmt.Sprintf("LOCALIZE[30287];;%s", movie.Title), config.AddonIcon())
		return nil, errGet
	}

	if _, err := writeMovieStrm(tmdbID); err != nil {
		return movie, err
	}

	if err := updateDB(Update, MovieType, []int{ID}, 0); err != nil {
		return movie, err
	}
	if err := updateDB(Delete, RemovedMovieType, []int{ID}, 0); err != nil {
		return movie, err
	}

	log.Noticef("%s added to library", movie.Title)
	return movie, nil
}

// AddShow is adding show to the library
func AddShow(tmdbID string, merge string) (*tmdb.Show, error) {
	if err := checkShowsPath(); err != nil {
		return nil, err
	}

	ID, _ := strconv.Atoi(tmdbID)
	show, errGet := IsDuplicateShow(tmdbID)
	if merge == falseType && errGet != nil {
		log.Warning(errGet)
		xbmc.Notify("Elementum", fmt.Sprintf("LOCALIZE[30287];;%s", show.Name), config.AddonIcon())
		return show, errGet
	}

	if _, err := writeShowStrm(tmdbID, true); err != nil {
		log.Error(err)
		return show, err
	}

	if err := updateDB(Update, ShowType, []int{ID}, 0); err != nil {
		return show, err
	}
	if err := updateDB(Delete, RemovedShowType, []int{ID}, 0); err != nil {
		return show, err
	}

	return show, nil
}

// GetMovie returns LibraryItem for kodi id
func GetMovie(kodiID int) *xbmc.VideoLibraryMovieItem {
	l.mu.Movies.Lock()
	defer l.mu.Movies.Unlock()

	for _, m := range l.Movies {
		if m.UIDs.Kodi == kodiID {
			return m.Xbmc
		}
	}

	return nil
}

// GetEpisode returns LibraryItem for kodi id
func GetEpisode(kodiID int) *xbmc.VideoLibraryEpisodeItem {
	l.mu.Shows.RLock()
	defer l.mu.Shows.RUnlock()

	for _, existingShow := range l.Shows {
		for _, existingEpisode := range existingShow.Episodes {
			if existingEpisode.UIDs.Kodi == kodiID {
				return existingEpisode.Xbmc
			}
		}
	}

	return nil
}

// GetUIDsFromKodi returns UIDs object for provided Kodi ID
func GetUIDsFromKodi(kodiID int) *UniqueIDs {
	if kodiID == 0 {
		return nil
	}

	l.mu.UIDs.Lock()
	defer l.mu.UIDs.Unlock()

	for _, u := range l.UIDs {
		if u.Kodi == kodiID {
			return u
		}
	}

	return nil
}

// GetShowForEpisode returns 'show' and 'episode'
func GetShowForEpisode(kodiID int) (*Show, *Episode) {
	if kodiID == 0 {
		return nil, nil
	}

	l.mu.Shows.Lock()
	defer l.mu.Shows.Unlock()

	for _, s := range l.Shows {
		for _, e := range s.Episodes {
			if e.UIDs.Kodi == kodiID {
				return s, e
			}
		}
	}

	return nil, nil
}
