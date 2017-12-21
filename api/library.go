package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/elgatito/elementum/bittorrent"
	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/database"
	"github.com/elgatito/elementum/tmdb"
	"github.com/elgatito/elementum/trakt"
	"github.com/elgatito/elementum/util"
	"github.com/elgatito/elementum/xbmc"
	"github.com/gin-gonic/gin"
	"github.com/op/go-logging"
)

const (
	movieType   = "movie"
	showType    = "show"
	episodeType = "episode"

	playLabel  = "LOCALIZE[30023]"
	linksLabel = "LOCALIZE[30202]"

	statusQueued      = "Queued"
	statusDownloading = "Downloading"
	statusSeeding     = "Seeding"
	statusFinished    = "Finished"
	statusPaused      = "Paused"
	statusFinding     = "Finding"
	statusBuffering   = "Buffering"
	statusAllocating  = "Allocating"
	statusStalled     = "Stalled"
	statusChecking    = "Checking"

	trueType  = "true"
	falseType = "false"

	multiType = "\nmulti"
)

const (
	// Movie ...
	Movie = iota
	// Show ...
	Show
	// Season ...
	Season
	// Episode ...
	Episode
	// RemovedMovie ...
	RemovedMovie
	// RemovedShow ...
	RemovedShow
	// RemovedSeason ...
	RemovedSeason
	// RemovedEpisode ...
	RemovedEpisode
)

const (
	// TVDBScraper ...
	TVDBScraper = iota
	// TMDBScraper ...
	TMDBScraper
	// TraktScraper ...
	TraktScraper
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
	libraryLog        = logging.MustGetLogger("library")
	libraryEpisodes   = make(map[int]*xbmc.VideoLibraryEpisodes)
	libraryShows      *xbmc.VideoLibraryShows
	libraryMovies     *xbmc.VideoLibraryMovies
	libraryPath       string
	moviesLibraryPath string
	showsLibraryPath  string
	db                *database.Database
	// Bucket ...
	Bucket          = database.LibraryBucket
	closing         = make(chan struct{})
	removedEpisodes = make(chan *removedEpisode)
	scanning        = false
)

// DBItem ...
type DBItem struct {
	ID       string `json:"id"`
	Type     int    `json:"type"`
	TVShowID int    `json:"showid"`
}

type removedEpisode struct {
	ID        string
	ShowID    string
	ScraperID string
	ShowName  string
	Season    int
	Episode   int
}

// InitDB ...
func InitDB() {
	db, _ = database.NewDB()
}

func clearPageCache(ctx *gin.Context) {
	if ctx != nil {
		ctx.Abort()
	}
	files, _ := filepath.Glob(filepath.Join(config.Get().Info.Profile, "cache", "page.*"))
	for _, file := range files {
		os.Remove(file)
	}
	xbmc.Refresh()
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
			libraryLog.Error(err)
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
			libraryLog.Error(err)
			return err
		}
	}
	return nil
}

//
// Updates from Kodi library
//
func updateLibraryMovies() {
	libraryMovies = xbmc.VideoLibraryGetMovies()
}
func updateLibraryShows() {
	libraryShows = xbmc.VideoLibraryGetShows()
	if libraryShows == nil {
		return
	}
	for _, tvshow := range libraryShows.Shows {
		updateLibraryEpisodes(tvshow.ID)
	}
}
func updateLibraryEpisodes(showID int) {
	libraryEpisodes[showID] = xbmc.VideoLibraryGetEpisodes(showID)
}

//
// Watched handling
//

// UpdateMovieWatched ...
func UpdateMovieWatched(itemID int, watchedTime float64, videoDuration float64) {
	progress := watchedTime / videoDuration * 100

	libraryLog.Infof("Currently at %f%%, DBID: %d", progress, itemID)

	if progress > 90 {
		xbmc.SetMovieWatched(itemID, 1, 0, 0)
	} else if watchedTime > 180 {
		xbmc.SetMovieWatched(itemID, 0, int(watchedTime), int(videoDuration))
	} else {
		time.Sleep(200 * time.Millisecond)
		xbmc.Refresh()
	}
}

// UpdateEpisodeWatched ...
func UpdateEpisodeWatched(itemID int, watchedTime float64, videoDuration float64) {
	progress := watchedTime / videoDuration * 100

	libraryLog.Infof("Currently at %f%%, DBID: %d", progress, itemID)

	if progress > 90 {
		xbmc.SetEpisodeWatched(itemID, 1, 0, 0)
	} else if watchedTime > 180 {
		xbmc.SetEpisodeWatched(itemID, 0, int(watchedTime), int(videoDuration))
	} else {
		time.Sleep(200 * time.Millisecond)
		xbmc.Refresh()
	}
}

//
// Duplicate handling
//
func isDuplicateMovie(tmdbID string) (*tmdb.Movie, error) {
	movie := tmdb.GetMovieByID(tmdbID, "en")
	if movie == nil || movie.IMDBId == "" {
		return movie, nil
	}
	if libraryMovies == nil {
		return movie, nil
	}
	for _, existingMovie := range libraryMovies.Movies {
		if existingMovie.IMDBNumber != "" {
			if existingMovie.IMDBNumber == movie.IMDBId {
				return movie, fmt.Errorf("%s already in library", movie.Title)
			}
		}
	}
	return movie, nil
}

func isDuplicateShow(tmdbID string) (*tmdb.Show, error) {
	show := tmdb.GetShowByID(tmdbID, "en")
	showID := GetShowUniqueID(show)

	if len(showID) == 0 || libraryShows == nil {
		return show, nil
	}
	for _, existingShow := range libraryShows.Shows {
		// TODO Aho-Corasick name matching to allow mixed scraper sources
		if existingShow.ScraperID == showID {
			return show, fmt.Errorf("%s already in library", show.Name)
		}
	}
	return show, nil
}

func isDuplicateEpisode(tmdbShowID int, seasonNumber int, episodeNumber int) (episodeID string, err error) {
	episode := tmdb.GetEpisode(tmdbShowID, seasonNumber, episodeNumber, "en")
	noExternalIDs := fmt.Sprintf("No external IDs found for S%02dE%02d (%d)", seasonNumber, episodeNumber, tmdbShowID)
	if episode == nil || episode.ExternalIDs == nil {
		libraryLog.Warning(noExternalIDs + ". No ExternalIDs")
		return
	}

	episodeID = strconv.Itoa(episode.ID)
	switch config.Get().TvScraper {
	case TMDBScraper:
		break
	case TVDBScraper:
		if episode.ExternalIDs == nil || episode.ExternalIDs.TVDBID == nil {
			libraryLog.Warningf(noExternalIDs + ". No ExternalIDs for TVDB")
			return
		}
		episodeID = strconv.Itoa(util.StrInterfaceToInt(episode.ExternalIDs.TVDBID))
	case TraktScraper:
		traktEpisode := trakt.GetEpisodeByTMDB(episodeID)
		if traktEpisode == nil || traktEpisode.IDs == nil || traktEpisode.IDs.Trakt == 0 {
			libraryLog.Warning(noExternalIDs + " from Trakt episode")
			return
		}
		episodeID = strconv.Itoa(traktEpisode.IDs.Trakt)
	}

	var showID string
	switch config.Get().TvScraper {
	case TMDBScraper:
		showID = strconv.Itoa(tmdbShowID)
	case TVDBScraper:
		show := tmdb.GetShowByID(strconv.Itoa(tmdbShowID), "en")
		if show.ExternalIDs == nil || show.ExternalIDs.TVDBID == nil {
			libraryLog.Warning(noExternalIDs + " for TVDB show")
			return
		}
		showID = strconv.Itoa(util.StrInterfaceToInt(show.ExternalIDs.TVDBID))
	case TraktScraper:
		traktShow := trakt.GetShowByTMDB(strconv.Itoa(tmdbShowID))
		if traktShow == nil || traktShow.IDs == nil || traktShow.IDs.Trakt == 0 {
			libraryLog.Warning(noExternalIDs + " from Trakt show")
			return
		}
		showID = strconv.Itoa(traktShow.IDs.Trakt)
	}

	var tvshowID int
	if libraryShows == nil {
		return
	}
	for _, existingShow := range libraryShows.Shows {
		if existingShow.ScraperID == showID {
			tvshowID = existingShow.ID
			break
		}
	}
	if tvshowID == 0 {
		return
	}

	if libraryEpisodes == nil {
		return
	}
	if episodes, exists := libraryEpisodes[tvshowID]; exists {
		if episodes == nil {
			return
		}
		for _, existingEpisode := range episodes.Episodes {
			if existingEpisode.UniqueIDs.ID == episodeID ||
				(existingEpisode.Season == seasonNumber && existingEpisode.Episode == episodeNumber) {
				err = fmt.Errorf("%s S%02dE%02d already in library", existingEpisode.Title, seasonNumber, episodeNumber)
				return
			}
		}
	} else {
		libraryLog.Warningf("Missing tvshowid (%d) in library episodes for S%02dE%02d (%s)", tvshowID, seasonNumber, episodeNumber, showID)
	}
	return
}

func isAddedToLibrary(id string, addedType int) (isAdded bool) {
	db.Seek(Bucket, fmt.Sprintf("%d_", addedType), func(key []byte, value []byte) {
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
func updateDB(Operation int, Type int, IDs []string, TVShowID int) error {
	switch Operation {
	case Update:
		item := DBItem{
			ID:       IDs[0],
			Type:     Type,
			TVShowID: TVShowID,
		}

		return db.SetObject(Bucket, fmt.Sprintf("%d_%s", Type, IDs[0]), item)
	case Batch:
		objects := map[string]interface{}{}
		for _, id := range IDs {
			item := DBItem{
				ID:       id,
				Type:     Type,
				TVShowID: TVShowID,
			}
			objects[fmt.Sprintf("%d_%s", Type, id)] = item
		}

		return db.BatchSetObject(Bucket, objects)
	case Delete:
		return db.Delete(Bucket, fmt.Sprintf("%d_%s", Type, IDs[0]))
	case BatchDelete:
		items := make([]string, len(IDs))
		for i, key := range IDs {
			items[i] = fmt.Sprintf("%d_%s", Type, key)
		}

		return db.BatchDelete(Bucket, items)
	case DeleteTorrent:
		return db.Delete(bittorrent.Bucket, IDs[0])
	}

	return nil
}

func wasRemoved(id string, removedType int) (wasRemoved bool) {
	if v, err := db.Get(Bucket, fmt.Sprintf("%d_%s", removedType, id)); err == nil && len(v) > 0 {
		wasRemoved = true
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

	db.Seek(Bucket, fmt.Sprintf("%d_", Show), func(key []byte, value []byte) {
		item := &DBItem{}
		if err := json.Unmarshal(value, &item); err != nil {
			return
		}
		if _, err := writeShowStrm(item.ID, false); err != nil {
			libraryLog.Error(err)
		}
	})

	libraryLog.Notice("Library updated")
	return nil
}

func doSyncTrakt() error {
	if err := checkMoviesPath(); err != nil {
		return err
	}
	if err := checkShowsPath(); err != nil {
		return err
	}

	if err := syncMoviesList("watchlist", true); err != nil {
		return err
	}
	if err := syncMoviesList("collection", true); err != nil {
		return err
	}
	if err := syncShowsList("watchlist", true); err != nil {
		return err
	}
	if err := syncShowsList("collection", true); err != nil {
		return err
	}

	lists := trakt.Userlists()
	for _, list := range lists {
		if err := syncMoviesList(strconv.Itoa(list.IDs.Trakt), true); err != nil {
			continue
		}
		if err := syncShowsList(strconv.Itoa(list.IDs.Trakt), true); err != nil {
			continue
		}
	}

	libraryLog.Notice("Trakt lists synced")
	return nil
}

//
// Movie internals
//
func syncMoviesList(listID string, updating bool) (err error) {
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
		libraryLog.Error(err)
		return
	}

	var movieIDs []string
	for _, movie := range movies {
		title := movie.Movie.Title
		if movie.Movie.IDs.TMDB == 0 {
			libraryLog.Warningf("Missing TMDB ID for %s", title)
			continue
		}

		tmdbID := strconv.Itoa(movie.Movie.IDs.TMDB)

		if updating && wasRemoved(tmdbID, RemovedMovie) {
			continue
		}

		if _, err := isDuplicateMovie(tmdbID); err != nil {
			if !updating {
				libraryLog.Warning(err)
			}
			continue
		}

		if _, err := writeMovieStrm(tmdbID); err != nil {
			libraryLog.Error(err)
			continue
		}

		movieIDs = append(movieIDs, tmdbID)
	}

	if err := updateDB(Batch, Movie, movieIDs, 0); err != nil {
		return err
	}

	if !updating {
		libraryLog.Noticef("Movies list (%s) added", listID)
		if xbmc.DialogConfirm("Elementum", fmt.Sprintf("LOCALIZE[30277];;%s", label)) {
			xbmc.VideoLibraryScan()
		}
	}
	return nil
}

func writeMovieStrm(tmdbID string) (*tmdb.Movie, error) {
	movie := tmdb.GetMovieByID(tmdbID, "en")
	if movie == nil {
		return movie, fmt.Errorf("Unable to get movie (%s)", tmdbID)
	}

	movieStrm := util.ToFileName(fmt.Sprintf("%s (%s)", movie.OriginalTitle, strings.Split(movie.ReleaseDate, "-")[0]))
	moviePath := filepath.Join(moviesLibraryPath, movieStrm)

	if _, err := os.Stat(moviePath); os.IsNotExist(err) {
		if err := os.Mkdir(moviePath, 0755); err != nil {
			libraryLog.Error(err)
			return movie, err
		}
	}

	movieStrmPath := filepath.Join(moviePath, fmt.Sprintf("%s.strm", movieStrm))

	playLink := URLForXBMC("/library/movie/play/%s", tmdbID)
	if _, err := os.Stat(movieStrmPath); err == nil {
		return movie, fmt.Errorf("LOCALIZE[30287];;%s", movie.Title)
	}
	if err := ioutil.WriteFile(movieStrmPath, []byte(playLink), 0644); err != nil {
		libraryLog.Error(err)
		return movie, err
	}

	return movie, nil
}

func removeMovie(ctx *gin.Context, tmdbID string) error {
	if err := checkMoviesPath(); err != nil {
		return err
	}
	movie := tmdb.GetMovieByID(tmdbID, "en")
	movieName := fmt.Sprintf("%s (%s)", movie.OriginalTitle, strings.Split(movie.ReleaseDate, "-")[0])
	movieStrm := util.ToFileName(movieName)
	moviePath := filepath.Join(moviesLibraryPath, movieStrm)

	if _, err := os.Stat(moviePath); err != nil {
		return errors.New("LOCALIZE[30282]")
	}
	if err := os.RemoveAll(moviePath); err != nil {
		return err
	}

	if err := updateDB(Delete, Movie, []string{tmdbID}, 0); err != nil {
		return err
	}
	if err := updateDB(Update, RemovedMovie, []string{tmdbID}, 0); err != nil {
		return err
	}
	libraryLog.Warningf("%s removed from library", movieName)

	if ctx != nil {
		if xbmc.DialogConfirm("Elementum", fmt.Sprintf("LOCALIZE[30278];;%s", movieName)) {
			xbmc.VideoLibraryClean()
		} else {
			clearPageCache(ctx)
		}
	}

	return nil
}

//
// Shows internals
//
func syncShowsList(listID string, updating bool) (err error) {
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
		libraryLog.Error(err)
		return
	}

	var showIDs []string
	for _, show := range shows {
		title := show.Show.Title
		if show.Show.IDs.TMDB == 0 {
			libraryLog.Warningf("Missing TMDB ID for %s", title)
			continue
		}

		tmdbID := strconv.Itoa(show.Show.IDs.TMDB)

		if updating && wasRemoved(tmdbID, RemovedShow) {
			continue
		}

		if !updating {
			if _, err := isDuplicateShow(tmdbID); err != nil {
				libraryLog.Warning(err)
				continue
			}
		}

		if _, err := writeShowStrm(tmdbID, false); err != nil {
			libraryLog.Error(err)
			continue
		}

		showIDs = append(showIDs, tmdbID)
	}

	if err := updateDB(Batch, Show, showIDs, 0); err != nil {
		return err
	}

	if !updating {
		libraryLog.Noticef("Shows list (%s) added", listID)
		if xbmc.DialogConfirm("Elementum", fmt.Sprintf("LOCALIZE[30277];;%s", label)) {
			xbmc.VideoLibraryScan()
		}
	}
	return nil
}

func writeShowStrm(showID string, adding bool) (*tmdb.Show, error) {
	ID, _ := strconv.Atoi(showID)
	show := tmdb.GetShow(ID, "en")
	if show == nil {
		return nil, fmt.Errorf("Unable to get show (%s)", showID)
	}
	showStrm := util.ToFileName(fmt.Sprintf("%s (%s)", show.Name, strings.Split(show.FirstAirDate, "-")[0]))
	showPath := filepath.Join(showsLibraryPath, showStrm)

	if _, err := os.Stat(showPath); os.IsNotExist(err) {
		if err := os.Mkdir(showPath, 0755); err != nil {
			libraryLog.Error(err)
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

		episodes := tmdb.GetSeason(ID, season.Season, "en").Episodes

		var reAddIDs []string
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
				reAddIDs = append(reAddIDs, strconv.Itoa(episode.ID))
			} else {
				// Check if single episode was previously removed
				if wasRemoved(strconv.Itoa(episode.ID), RemovedEpisode) {
					continue
				}
			}

			if _, err := isDuplicateEpisode(ID, season.Season, episode.EpisodeNumber); err != nil {
				continue
			}

			episodeStrmPath := filepath.Join(showPath, fmt.Sprintf("%s S%02dE%02d.strm", showStrm, season.Season, episode.EpisodeNumber))
			playLink := URLForXBMC("/library/show/play/%d/%d/%d", ID, season.Season, episode.EpisodeNumber)
			if _, err := os.Stat(episodeStrmPath); err == nil {
				libraryLog.Warningf("%s already exists, skipping", episodeStrmPath)
				continue
			}
			if err := ioutil.WriteFile(episodeStrmPath, []byte(playLink), 0644); err != nil {
				libraryLog.Error(err)
				return show, err
			}
		}
		if len(reAddIDs) > 0 {
			if err := updateDB(BatchDelete, RemovedEpisode, reAddIDs, ID); err != nil {
				libraryLog.Error(err)
			}
		}
	}

	return show, nil
}

func removeShow(ctx *gin.Context, tmdbID string) error {
	if err := checkShowsPath(); err != nil {
		return err
	}
	ID, _ := strconv.Atoi(tmdbID)
	show := tmdb.GetShow(ID, "en")

	if show == nil {
		return errors.New("Unable to find show to remove")
	}

	showStrm := util.ToFileName(fmt.Sprintf("%s (%s)", show.Name, strings.Split(show.FirstAirDate, "-")[0]))
	showPath := filepath.Join(showsLibraryPath, showStrm)

	if _, err := os.Stat(showPath); err != nil {
		libraryLog.Warning(err)
		return errors.New("LOCALIZE[30282]")
	}
	if err := os.RemoveAll(showPath); err != nil {
		libraryLog.Error(err)
		return err
	}

	if err := updateDB(Delete, Show, []string{tmdbID}, 0); err != nil {
		return err
	}
	if err := updateDB(Update, RemovedShow, []string{tmdbID}, 0); err != nil {
		return err
	}
	libraryLog.Warningf("%s removed from library", show.Name)

	if ctx != nil {
		if xbmc.DialogConfirm("Elementum", fmt.Sprintf("LOCALIZE[30278];;%s", show.Name)) {
			xbmc.VideoLibraryClean()
		} else {
			clearPageCache(ctx)
		}
	}

	return nil
}

func removeEpisode(tmdbID string, showID string, scraperID string, seasonNumber int, episodeNumber int) error {
	if err := checkShowsPath(); err != nil {
		return err
	}
	ID, _ := strconv.Atoi(showID)
	show := tmdb.GetShow(ID, "en")

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
		libraryLog.Noticef("%s removed from library", episodeStrm)
	} else {
		return errors.New("Nothing left to remove from Elementum")
	}

	return nil
}

//
// Movie externals
//

// AddMovie ...
func AddMovie(ctx *gin.Context) {
	if err := checkMoviesPath(); err != nil {
		ctx.String(200, err.Error())
		return
	}
	tmdbID := ctx.Params.ByName("tmdbId")

	if movie, err := isDuplicateMovie(tmdbID); err != nil {
		libraryLog.Warningf(err.Error())
		xbmc.Notify("Elementum", fmt.Sprintf("LOCALIZE[30287];;%s", movie.Title), config.AddonIcon())
		return
	}

	var err error
	var movie *tmdb.Movie
	if movie, err = writeMovieStrm(tmdbID); err != nil {
		ctx.String(200, err.Error())
		return
	}

	if err := updateDB(Update, Movie, []string{tmdbID}, 0); err != nil {
		ctx.String(200, err.Error())
		return
	}
	if err := updateDB(Delete, RemovedMovie, []string{tmdbID}, 0); err != nil {
		ctx.String(200, err.Error())
		return
	}

	libraryLog.Noticef("%s added to library", movie.Title)
	if xbmc.DialogConfirm("Elementum", fmt.Sprintf("LOCALIZE[30277];;%s", movie.Title)) {
		xbmc.VideoLibraryScan()
	} else {
		clearPageCache(ctx)
	}
}

// AddMoviesList ...
func AddMoviesList(ctx *gin.Context) {
	listID := ctx.Params.ByName("listId")
	updatingStr := ctx.DefaultQuery("updating", falseType)

	updating := false
	if updatingStr != falseType {
		updating = true
	}

	syncMoviesList(listID, updating)
}

// RemoveMovie ...
func RemoveMovie(ctx *gin.Context) {
	tmdbID := ctx.Params.ByName("tmdbId")
	if err := removeMovie(ctx, tmdbID); err != nil {
		ctx.String(200, err.Error())
	}
}

//
// Shows externals
//

// AddShow ...
func AddShow(ctx *gin.Context) {
	if err := checkShowsPath(); err != nil {
		ctx.String(200, err.Error())
		return
	}
	tmdbID := ctx.Params.ByName("tmdbId")
	merge := ctx.DefaultQuery("merge", falseType)

	label := "LOCALIZE[30277]"
	logMsg := "%s (%s) added to library"
	if merge == falseType {
		if show, err := isDuplicateShow(tmdbID); err != nil {
			libraryLog.Warning(err)
			xbmc.Notify("Elementum", fmt.Sprintf("LOCALIZE[30287];;%s", show.Name), config.AddonIcon())
			return
		}
	} else {
		label = "LOCALIZE[30286]"
		logMsg = "%s (%s) merged to library"
	}

	var err error
	var show *tmdb.Show
	if show, err = writeShowStrm(tmdbID, true); err != nil {
		libraryLog.Error(err)
		ctx.String(200, err.Error())
		return
	}

	if err := updateDB(Update, Show, []string{tmdbID}, 0); err != nil {
		ctx.String(200, err.Error())
		return
	}
	if err := updateDB(Delete, RemovedShow, []string{tmdbID}, 0); err != nil {
		ctx.String(200, err.Error())
		return
	}

	libraryLog.Noticef(logMsg, show.Name, tmdbID)
	if xbmc.DialogConfirm("Elementum", fmt.Sprintf("%s;;%s", label, show.Name)) {
		xbmc.VideoLibraryScan()
	} else {
		clearPageCache(ctx)
	}
}

// AddShowsList ...
func AddShowsList(ctx *gin.Context) {
	listID := ctx.Params.ByName("listId")
	updatingStr := ctx.DefaultQuery("updating", falseType)

	updating := false
	if updatingStr != falseType {
		updating = true
	}

	syncShowsList(listID, updating)
}

// RemoveShow ...
func RemoveShow(ctx *gin.Context) {
	tmdbID := ctx.Params.ByName("tmdbId")
	if err := removeShow(ctx, tmdbID); err != nil {
		ctx.String(200, err.Error())
	}
}

//
// Library update loop
//

// LibraryUpdate ...
func LibraryUpdate() {
	if err := checkMoviesPath(); err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
		return
	}
	if err := checkShowsPath(); err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
		return
	}

	// Migrate old ElementumDB.json
	if _, err := os.Stat(filepath.Join(libraryPath, "ElementumDB.json")); err == nil {
		libraryLog.Warning("Found ElementumDB.json, upgrading to BoltDB...")
		var oldDB struct {
			Movies []string `json:"movies"`
			Shows  []string `json:"shows"`
		}
		oldFile := filepath.Join(libraryPath, "ElementumDB.json")
		file, err := ioutil.ReadFile(oldFile)
		if err != nil {
			xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
		} else {
			if err := json.Unmarshal(file, &oldDB); err != nil {
				xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
			} else if err := updateDB(Batch, Movie, oldDB.Movies, 0); err != nil {
				xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
			} else if err := updateDB(Batch, Show, oldDB.Shows, 0); err != nil {
				xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
			} else {
				os.Remove(oldFile)
				libraryLog.Notice("Successfully imported and removed ElementumDB.json")
			}
		}
	}

	go func() {
		// Give time to Kodi to start its JSON-RPC service
		time.Sleep(5 * time.Second)
		updateLibraryMovies()
		updateLibraryShows()
	}()

	// Start warming caches by pre-fetching popular/trending lists
	go func() {
		libraryLog.Notice("Warming up caches...")
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
			libraryLog.Warning(err)
		}
		if _, _, err := trakt.TopShows("trending", "1"); err != nil {
			libraryLog.Warning(err)
		}
		tmdb.WarmingUp = false
		took := time.Since(started)
		if took.Seconds() > 30 {
			xbmc.Notify("Elementum", "LOCALIZE[30148]", config.AddonIcon())
		}
		libraryLog.Noticef("Caches warmed up in %s", took)
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
						if libraryShows == nil {
							break
						}
						for _, libraryShow := range libraryShows.Shows {
							if libraryShow.ScraperID == showEpisodes[0].ScraperID {
								libraryLog.Warningf("Library removed %d episodes for %s", libraryShow.Episodes, libraryShow.Title)
								libraryTotal = libraryShow.Episodes
								break
							}
						}
						if libraryTotal == 0 {
							break
						}
						if len(showEpisodes) == libraryTotal {
							if err := removeShow(nil, showEpisodes[0].ShowID); err != nil {
								libraryLog.Error("Unable to remove show after removing all episodes...")
							}
						} else {
							labels = append(labels, fmt.Sprintf("%d episodes of %s", len(showEpisodes), showName))
						}

						// Add single episodes to removed prefix
						var tmdbIDs []string
						for _, showEpisode := range showEpisodes {
							tmdbIDs = append(tmdbIDs, showEpisode.ID)
						}
						ID, _ := strconv.Atoi(showEpisodes[0].ShowID)
						if err := updateDB(Batch, RemovedEpisode, tmdbIDs, ID); err != nil {
							libraryLog.Error(err)
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
						ID, _ := strconv.Atoi(episode[0].ShowID)
						if err := updateDB(Update, RemovedEpisode, []string{episode[0].ID}, ID); err != nil {
							libraryLog.Error(err)
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
					libraryLog.Warning(err)
				}
				if config.Get().UpdateAutoScan && scanning == false {
					scanning = true
					xbmc.VideoLibraryScan()
				}
			}
		}()
	}

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
					libraryLog.Warning(err)
				}
				if config.Get().UpdateAutoScan && scanning == false && updateFrequency != traktFrequency {
					scanning = true
					xbmc.VideoLibraryScan()
				}
			}
		case <-traktSyncTicker.C:
			if config.Get().TraktSyncFrequency > 0 {
				if err := doSyncTrakt(); err != nil {
					libraryLog.Warning(err)
				}
				if config.Get().UpdateAutoScan && scanning == false {
					scanning = true
					xbmc.VideoLibraryScan()
				}
			}
		case <-markedForRemovalTicker.C:
			db.ForEach(bittorrent.Bucket, func(key []byte, value []byte) error {
				item := &bittorrent.DBItem{}
				if err := json.Unmarshal(value, &item); err != nil {
					libraryLog.Error(err)
					return err
				}
				if item.State > bittorrent.Remove {
					return nil
				}

				// Remove from Elementum's library to prevent duplicates
				if item.Type == movieType {
					if _, err := isDuplicateMovie(strconv.Itoa(item.ID)); err != nil {
						removeMovie(nil, strconv.Itoa(item.ID))
						if err := removeMovie(nil, strconv.Itoa(item.ID)); err != nil {
							libraryLog.Warning("Nothing left to remove from Elementum")
						}
					}
				} else {
					if scraperID, err := isDuplicateEpisode(item.ShowID, item.Season, item.Episode); err != nil {
						if err := removeEpisode(strconv.Itoa(item.ID), strconv.Itoa(item.ShowID), scraperID, item.Season, item.Episode); err != nil {
							libraryLog.Warning(err)
						}
					}
				}
				updateDB(DeleteTorrent, 0, []string{string(key)}, 0)
				libraryLog.Infof("Removed %s from database", key)
				return nil
			})
		case <-closing:
			close(removedEpisodes)
			return
		}
	}
}

// UpdateLibrary ...
func UpdateLibrary(ctx *gin.Context) {
	if err := doUpdateLibrary(); err != nil {
		ctx.String(200, err.Error())
	}
	if xbmc.DialogConfirm("Elementum", "LOCALIZE[30288]") {
		xbmc.VideoLibraryScan()
	}
}

// UpdateTrakt ...
func UpdateTrakt(ctx *gin.Context) {
	if err := doSyncTrakt(); err != nil {
		ctx.String(200, err.Error())
	}
	if xbmc.DialogConfirm("Elementum", "LOCALIZE[30288]") {
		xbmc.VideoLibraryScan()
	}
}

// CloseLibrary ...
func CloseLibrary() {
	libraryLog.Info("Closing library...")
	close(closing)
}

//
// Library searchers
//

// FindByIDEpisodeInLibrary ...
func FindByIDEpisodeInLibrary(showID int, seasonNumber int, episodeNumber int) *xbmc.VideoLibraryEpisodeItem {
	show := tmdb.GetShow(showID, config.Get().Language)
	if show == nil {
		return nil
	}

	episode := tmdb.GetEpisode(showID, seasonNumber, episodeNumber, config.Get().Language)
	if episode != nil {
		return FindEpisodeInLibrary(show, episode)
	}

	return nil
}

// FindByIDMovieInLibrary ...
func FindByIDMovieInLibrary(id string) *xbmc.VideoLibraryMovieItem {
	movie := tmdb.GetMovieByID(id, config.Get().Language)
	if movie != nil {
		return FindMovieInLibrary(movie)
	}

	return nil
}

// FindMovieInLibrary ...
func FindMovieInLibrary(movie *tmdb.Movie) *xbmc.VideoLibraryMovieItem {
	if libraryMovies == nil {
		return nil
	}
	for _, existingMovie := range libraryMovies.Movies {
		if existingMovie.IMDBNumber != "" {
			if existingMovie.IMDBNumber == movie.IMDBId {
				return existingMovie
			}
		}
	}

	return nil
}

// FindEpisodeInLibrary ...
func FindEpisodeInLibrary(show *tmdb.Show, episode *tmdb.Episode) *xbmc.VideoLibraryEpisodeItem {
	if episode == nil || show == nil {
		return nil
	}

	episodeID := GetEpisodeUniqueID(show, episode)
	if len(episodeID) > 0 {
		for _, episodes := range libraryEpisodes {
			if episodes == nil || episodes.Episodes == nil {
				continue
			}

			for _, existingEpisode := range episodes.Episodes {
				if existingEpisode.UniqueIDs.ID == episodeID {
					return existingEpisode
				}
			}
		}
	}

	showID := GetShowUniqueID(show)
	if len(showID) > 0 {
		for _, episodes := range libraryEpisodes {
			if episodes == nil || episodes.Episodes == nil {
				continue
			}

			for _, existingEpisode := range episodes.Episodes {
				if strconv.Itoa(existingEpisode.TVShowID) == showID &&
					existingEpisode.Season == episode.SeasonNumber &&
					existingEpisode.Episode == episode.EpisodeNumber {
					return existingEpisode
				}
			}
		}
	}

	return nil
}

// GetShowUniqueID ...
func GetShowUniqueID(show *tmdb.Show) (showID string) {
	if show == nil || show.ExternalIDs == nil {
		return
	}

	tmdbID := strconv.Itoa(show.ID)
	switch config.Get().TvScraper {
	case TMDBScraper:
		showID = tmdbID
	case TVDBScraper:
		if show.ExternalIDs == nil || show.ExternalIDs.TVDBID == nil {
			libraryLog.Warningf("No external IDs for TVDB show from TMDB ID %s", tmdbID)
			return
		}
		showID = strconv.Itoa(util.StrInterfaceToInt(show.ExternalIDs.TVDBID))
	case TraktScraper:
		traktShow := trakt.GetShowByTMDB(tmdbID)
		if traktShow == nil || traktShow.IDs == nil || traktShow.IDs.Trakt == 0 {
			libraryLog.Warningf("No external IDs from Trakt show for TMDB ID %s", tmdbID)
			return
		}
		showID = strconv.Itoa(traktShow.IDs.Trakt)
	}

	return
}

// GetEpisodeUniqueID ...
func GetEpisodeUniqueID(show *tmdb.Show, episode *tmdb.Episode) (episodeID string) {
	noExternalIDs := fmt.Sprintf("No external IDs found for S%02dE%02d (%d)", episode.SeasonNumber, episode.EpisodeNumber, show.ID)
	if episode == nil || episode.ExternalIDs == nil {
		libraryLog.Warning(noExternalIDs)
		return
	}

	episodeID = strconv.Itoa(episode.ID)
	switch config.Get().TvScraper {
	case TMDBScraper:
		break
	case TVDBScraper:
		if episode.ExternalIDs == nil || episode.ExternalIDs.TVDBID == nil {
			libraryLog.Warningf(noExternalIDs)
			return
		}
		episodeID = strconv.Itoa(util.StrInterfaceToInt(episode.ExternalIDs.TVDBID))
	case TraktScraper:
		traktEpisode := trakt.GetEpisodeByTMDB(episodeID)
		if traktEpisode == nil || traktEpisode.IDs == nil || traktEpisode.IDs.Trakt == 0 {
			libraryLog.Warning(noExternalIDs + " from Trakt episode")
			return
		}
		episodeID = strconv.Itoa(traktEpisode.IDs.Trakt)
	}

	if len(episodeID) == 0 {
		libraryLog.Warning(noExternalIDs)
	}

	return
}

//
// Kodi notifications
//

// Notification ...
func Notification(btService *bittorrent.BTService) gin.HandlerFunc {
	return func(ctx *gin.Context) {

		sender := ctx.Query("sender")
		method := ctx.Query("method")
		data := ctx.Query("data")

		// jsData, _ := base64.StdEncoding.DecodeString(data)
		// libraryLog.Debugf("Notification: sender=%s method=%s data=%s", sender, method, jsData)

		if sender == "xbmc" {
			switch method {
			case "Player.OnSeek":
				if bittorrent.VideoDuration > 0 {
					bittorrent.Seeked = true
				}
				btService.PlayerSeek()

			case "Player.OnPause":
				if bittorrent.VideoDuration == 0 {
					return
				}
				if !bittorrent.Paused {
					bittorrent.Paused = true
				}

			case "Player.OnPlay":
				time.Sleep(1 * time.Second) // Let player get its WatchedTime and VideoDuration
				if bittorrent.VideoDuration == 0 {
					return
				}
				if bittorrent.Paused { // Prevent seeking when simply unpausing
					bittorrent.Paused = false
					return
				}
				if !bittorrent.FromLibrary {
					return
				}
				libraryResume := config.Get().LibraryResume
				if libraryResume == 0 {
					return
				}
				var started struct {
					Item struct {
						ID   int    `json:"id"`
						Type string `json:"type"`
					} `json:"item"`
				}
				jsonData, _ := base64.StdEncoding.DecodeString(data)
				if err := json.Unmarshal(jsonData, &started); err != nil {
					libraryLog.Error(err)
					return
				}
				var position float64
				if started.Item.Type == movieType {
					var movie *xbmc.VideoLibraryMovieItem
					if libraryMovies == nil {
						return
					}
					for _, libraryMovie := range libraryMovies.Movies {
						if libraryMovie.ID == started.Item.ID {
							movie = libraryMovie
							break
						}
					}
					if movie == nil || movie.ID == 0 {
						libraryLog.Warningf("No movie found with ID: %d", started.Item.ID)
						return
					}
					if libraryResume == 2 && ExistingTorrent(btService, movie.Title) == "" {
						return
					}
					position = movie.Resume.Position
				} else {
					if libraryEpisodes == nil {
						return
					}
					var episode *xbmc.VideoLibraryEpisodeItem
					for _, episodes := range libraryEpisodes {
						if episodes == nil || episodes.Episodes == nil {
							continue
						}

						for _, existingEpisode := range episodes.Episodes {
							if existingEpisode.ID == started.Item.ID {
								episode = existingEpisode
								break
							}
						}
						if episode != nil {
							break
						}
					}
					if episode == nil || episode.ID == 0 {
						libraryLog.Warningf("No episode found with ID: %d", started.Item.ID)
						return
					}
					if libraryShows == nil {
						return
					}
					showTitle := ""
					for _, show := range libraryShows.Shows {
						if show.ScraperID == strconv.Itoa(episode.TVShowID) {
							showTitle = show.Title
						}
					}
					longName := fmt.Sprintf("%s S%02dE%02d", showTitle, episode.Season, episode.Episode)
					if libraryResume == 2 && ExistingTorrent(btService, longName) == "" {
						return
					}
					position = episode.Resume.Position
				}
				xbmc.PlayerSeek(position)

			case "Player.OnStop":
				if bittorrent.VideoDuration <= 1 {
					return
				}
				var stopped struct {
					Ended bool `json:"end"`
					Item  struct {
						ID   int    `json:"id"`
						Type string `json:"type"`
					} `json:"item"`
				}
				jsonData, _ := base64.StdEncoding.DecodeString(data)
				if err := json.Unmarshal(jsonData, &stopped); err != nil {
					libraryLog.Error(err)
					return
				}

				progress := bittorrent.WatchedTime / bittorrent.VideoDuration * 100

				libraryLog.Infof("Stopped at %f%%", progress)

				if stopped.Ended && progress > 90 {
					if stopped.Item.Type == movieType {
						xbmc.SetMovieWatched(stopped.Item.ID, 1, 0, 0)
					} else {
						xbmc.SetEpisodeWatched(stopped.Item.ID, 1, 0, 0)
					}
				} else if bittorrent.WatchedTime > 180 {
					if stopped.Item.Type == movieType {
						xbmc.SetMovieWatched(stopped.Item.ID, 0, int(bittorrent.WatchedTime), int(bittorrent.VideoDuration))
					} else {
						xbmc.SetEpisodeWatched(stopped.Item.ID, 0, int(bittorrent.WatchedTime), int(bittorrent.VideoDuration))
					}
				} else {
					time.Sleep(200 * time.Millisecond)
					xbmc.Refresh()
				}

			case "VideoLibrary.OnUpdate":
				time.Sleep(200 * time.Millisecond) // Because Kodi...
				if !bittorrent.WasPlaying {
					return
				}
				var item struct {
					ID   int    `json:"id"`
					Type string `json:"type"`
				}
				jsonData, _ := base64.StdEncoding.DecodeString(data)
				if err := json.Unmarshal(jsonData, &item); err != nil {
					libraryLog.Error(err)
					return
				}
				if item.ID != 0 {
					bittorrent.WasPlaying = false
					if item.Type == movieType {
						updateLibraryMovies()
					} else {
						updateLibraryShows()
					}
					xbmc.Refresh()
				}

			case "VideoLibrary.OnRemove":
				jsonData, err := base64.StdEncoding.DecodeString(data)
				if err != nil {
					libraryLog.Error(err)
					return
				}

				var item struct {
					ID   int    `json:"id"`
					Type string `json:"type"`
				}
				if err := json.Unmarshal(jsonData, &item); err != nil {
					libraryLog.Error(err)
					return
				}

				switch item.Type {
				case "episode":
					var episode *xbmc.VideoLibraryEpisodeItem
					if libraryEpisodes == nil {
						break
					}
					for _, episodes := range libraryEpisodes {
						if episodes == nil || episodes.Episodes == nil {
							continue
						}

						for _, existingEpisode := range episodes.Episodes {
							if existingEpisode.ID == item.ID {
								episode = existingEpisode
								break
							}
						}
					}
					if episode == nil || episode.ID == 0 {
						libraryLog.Warningf("No episode found with ID: %d", item.ID)
						return
					}

					var scraperID string
					if libraryShows == nil {
						break
					}
					for _, tvshow := range libraryShows.Shows {
						if tvshow.ID == episode.TVShowID {
							scraperID = tvshow.ScraperID
							break
						}
					}

					if scraperID != "" && episode.UniqueIDs.ID != "" {
						var tmdbID string
						var showID string

						switch config.Get().TvScraper {
						case TMDBScraper:
							tmdbID = episode.UniqueIDs.ID
							showID = scraperID
						case TVDBScraper:
							traktShow := trakt.GetShowByTVDB(scraperID)
							if traktShow == nil {
								libraryLog.Warning("No matching TVDB show to remove (%s)", scraperID)
								return
							}
							showID = strconv.Itoa(traktShow.IDs.TVDB)
							TVDBEpisode := trakt.GetEpisodeByTVDB(episode.UniqueIDs.ID)
							if TVDBEpisode == nil {
								libraryLog.Warning("No matching TVDB episode to remove (%s)", scraperID)
								return
							}
							tmdbID = strconv.Itoa(TVDBEpisode.IDs.TMDB)
						case TraktScraper:
							traktShow := trakt.GetShow(scraperID)
							if traktShow == nil {
								libraryLog.Warning("No matching show to remove (%s)", scraperID)
								return
							}
							showID = strconv.Itoa(traktShow.IDs.Trakt)
							traktEpisode := trakt.GetEpisode(episode.UniqueIDs.ID)
							if traktEpisode == nil {
								libraryLog.Warning("No matching episode to remove (%s)", scraperID)
								return
							}
							libraryLog.Warning("No matching episode to remove (%s)", episode.UniqueIDs.ID)
							return
						}

						if err := removeEpisode(tmdbID, showID, scraperID, episode.Season, episode.Episode); err != nil {
							libraryLog.Warning(err)
						}
					} else {
						libraryLog.Warning("Missing episodeid or tvshowid, nothing to remove")
					}
				case movieType:
					if libraryMovies == nil {
						break
					}
					for _, movie := range libraryMovies.Movies {
						if movie.ID == item.ID {
							tmdbMovie := tmdb.GetMovieByID(movie.IMDBNumber, "en")
							if tmdbMovie == nil || tmdbMovie.ID == 0 {
								break
							}
							if err := removeMovie(nil, strconv.Itoa(tmdbMovie.ID)); err != nil {
								libraryLog.Warning("Nothing left to remove from Elementum")
							}
							break
						}
					}
				}

			case "VideoLibrary.OnScanFinished":
				scanning = false
				fallthrough

			case "VideoLibrary.OnCleanFinished":
				updateLibraryMovies()
				updateLibraryShows()
				clearPageCache(ctx)
			}
		}
	}
}

// PlayMovie ...
func PlayMovie(btService *bittorrent.BTService) gin.HandlerFunc {
	if config.Get().ChooseStreamAuto == true {
		return MoviePlay(btService, true)
	}
	return MovieLinks(btService, true)
}

// PlayShow ...
func PlayShow(btService *bittorrent.BTService) gin.HandlerFunc {
	if config.Get().ChooseStreamAuto == true {
		return ShowEpisodePlay(btService, true)
	}
	return ShowEpisodeLinks(btService, true)
}
