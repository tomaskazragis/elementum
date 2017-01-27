package api

import (
	"os"
	"fmt"
	"time"
	"bytes"
	"errors"
	"strconv"
	"strings"
	"io/ioutil"
	"path/filepath"
	"encoding/json"
	"encoding/base64"

	"github.com/boltdb/bolt"
	"github.com/gin-gonic/gin"
	"github.com/op/go-logging"
	"github.com/scakemyer/quasar/util"
	"github.com/scakemyer/quasar/xbmc"
	"github.com/scakemyer/quasar/tmdb"
	"github.com/scakemyer/quasar/trakt"
	"github.com/scakemyer/quasar/config"
	"github.com/scakemyer/quasar/bittorrent"
)

const (
	Movie = iota
	Show
	Season
	Episode
	RemovedMovie
	RemovedShow
	RemovedSeason
	RemovedEpisode
)

const (
	TVDBScraper = iota
	TMDBScraper
	TraktScraper
)

const (
	Delete = iota
	Update
	Batch
	BatchDelete
)

var (
	libraryLog        = logging.MustGetLogger("library")
	libraryEpisodes   = make(map[int]*xbmc.VideoLibraryEpisodes)
	libraryShows      *xbmc.VideoLibraryShows
	libraryMovies     *xbmc.VideoLibraryMovies
	libraryPath       string
	moviesLibraryPath string
	showsLibraryPath  string
	DB                *bolt.DB
	bucket            = "Library"
	closing           = make(chan struct{})
)

type DBItem struct {
	ID       string `json:"id"`
	Type     int    `json:"type"`
	TVShowID int    `json:"showid"`
}

func clearPageCache(ctx *gin.Context) {
	ctx.Abort()
	files, _ := filepath.Glob(filepath.Join(config.Get().Info.Profile, "cache", "quasar.page.cache:*"))
	for _, file := range files {
		os.Remove(file)
	}
}

func toFileName(filename string) string {
	reserved := []string{"<", ">", ":", "\"", "/", "\\", "|", "?", "*", "%", "+"}
	for _, reservedchar := range reserved {
		filename = strings.Replace(filename, reservedchar, "", -1)
	}
	return filename
}

//
// Path checks
//
func checkLibraryPath() error {
	if libraryPath == "" {
		libraryPath = config.Get().LibraryPath
	}
	if fileInfo, err := os.Stat(libraryPath); err != nil || fileInfo.IsDir() == false || libraryPath == "" || libraryPath == "." {
		xbmc.Notify("Quasar", "LOCALIZE[30220]", config.AddonIcon())
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
	for _, tvshow := range libraryShows.Shows {
		updateLibraryEpisodes(tvshow.ID)
	}
}
func updateLibraryEpisodes(showId int) {
	libraryEpisodes[showId] = xbmc.VideoLibraryGetEpisodes(showId)
}

//
// Duplicate handling
//
func isDuplicateMovie(tmdbId string) error {
	movie := tmdb.GetMovieById(tmdbId, "en")
	if movie == nil || movie.IMDBId == "" {
		return nil
	}
	for _, existingMovie := range libraryMovies.Movies {
		if existingMovie.IMDBNumber != "" {
			if existingMovie.IMDBNumber == movie.IMDBId {
				return errors.New(fmt.Sprintf("%s already in library", movie.Title))
			}
		}
	}
	return nil
}

func isDuplicateShow(tmdbId string) error {
	show := tmdb.GetShowById(tmdbId, "en")
	if show == nil || show.ExternalIDs == nil {
		return nil
	}
	var showId string
	switch config.Get().TvScraper {
	case TMDBScraper:
		showId = tmdbId
	case TVDBScraper:
		if show.ExternalIDs == nil || show.ExternalIDs.TVDBID == nil {
			return nil
		}
		showId = strconv.Itoa(util.StrInterfaceToInt(show.ExternalIDs.TVDBID))
	case TraktScraper:
		traktShow := trakt.GetShowByTMDB(tmdbId)
		showId = strconv.Itoa(traktShow.IDs.Trakt)
	}
	for _, existingShow := range libraryShows.Shows {
		// TODO Aho-Corasick name matching to allow mixed scraper sources
		if existingShow.IMDBNumber == showId {
			return errors.New(fmt.Sprintf("%s already in library", show.Name))
		}
	}
	return nil
}

func isDuplicateEpisode(tmdbShowId int, seasonNumber int, episodeNumber int) error {
	episode := tmdb.GetEpisode(tmdbShowId, seasonNumber, episodeNumber, "en")
	if episode == nil || episode.ExternalIDs == nil {
		libraryLog.Warningf("No external IDs found for S%02dE%02d (%d)", seasonNumber, episodeNumber, tmdbShowId)
		return nil
	}

	episodeId := strconv.Itoa(episode.Id)
	switch config.Get().TvScraper {
	case TMDBScraper:
		break
	case TVDBScraper:
		episodeId = strconv.Itoa(util.StrInterfaceToInt(episode.ExternalIDs.TVDBID))
	case TraktScraper:
		traktEpisode := trakt.GetEpisodeByTMDB(episodeId)
		episodeId = strconv.Itoa(traktEpisode.IDs.Trakt)
	}

	var showId string
	switch config.Get().TvScraper {
	case TMDBScraper:
		showId = strconv.Itoa(tmdbShowId)
	case TVDBScraper:
		show := tmdb.GetShowById(strconv.Itoa(tmdbShowId), "en")
		if show.ExternalIDs == nil || show.ExternalIDs.TVDBID == nil {
			return nil
		}
		showId = strconv.Itoa(util.StrInterfaceToInt(show.ExternalIDs.TVDBID))
	case TraktScraper:
		traktShow := trakt.GetShowByTMDB(strconv.Itoa(tmdbShowId))
		showId = strconv.Itoa(traktShow.IDs.Trakt)
	}

	var tvshowId int
	for _, existingShow := range libraryShows.Shows {
		if existingShow.IMDBNumber == showId {
			tvshowId = existingShow.ID
			break
		}
	}
	if tvshowId == 0 {
		return nil
	}

	if episodes, exists := libraryEpisodes[tvshowId]; exists {
		for _, existingEpisode := range episodes.Episodes {
			if existingEpisode.UniqueIDs.ID == episodeId ||
				 (existingEpisode.Season == seasonNumber && existingEpisode.Episode == episodeNumber) {
				return errors.New(fmt.Sprintf("%s S%02dE%02d already in library", existingEpisode.Title, seasonNumber, episodeNumber))
			}
		}
	} else {
		libraryLog.Warningf("Missing tvshowid (%d) in library episodes for S%02dE%02d (%s)", tvshowId, seasonNumber, episodeNumber, showId)
	}
	return nil
}

//
// Database updates
//
func updateDB(Operation int, Type int, IDs []string, TVShowID int) (err error) {
	switch Operation {
	case Delete:
		err = DB.Update(func(tx *bolt.Tx) error {
			b := tx.Bucket([]byte(bucket))
			if err := b.Delete([]byte(fmt.Sprintf("%d_%s", Type, IDs[0]))); err != nil {
				return err
			}
			return nil
		})
	case Update:
		err = DB.Update(func(tx *bolt.Tx) error {
			b := tx.Bucket([]byte(bucket))
			item := DBItem{
				ID: IDs[0],
				Type: Type,
				TVShowID: TVShowID,
			}
			if buf, err := json.Marshal(item); err != nil {
				return err
			} else if err := b.Put([]byte(fmt.Sprintf("%d_%s", Type, IDs[0])), buf); err != nil {
				return err
			}
			return nil
		})
	case Batch:
		err = DB.Batch(func(tx *bolt.Tx) error {
			b := tx.Bucket([]byte(bucket))
			for _, id := range IDs {
				item := DBItem{
					ID: id,
					Type: Type,
					TVShowID: TVShowID,
				}
				if buf, err := json.Marshal(item); err != nil {
					libraryLog.Error(err)
					return err
				} else if err := b.Put([]byte(fmt.Sprintf("%d_%s", Type, id)), buf); err != nil {
					libraryLog.Error(err)
					return err
				}
			}
			return nil
		})
	case BatchDelete:
		err = DB.Batch(func(tx *bolt.Tx) error {
			b := tx.Bucket([]byte(bucket))
			for _, id := range IDs {
				if err := b.Delete([]byte(fmt.Sprintf("%d_%s", Type, id))); err != nil {
					return err
				}
			}
			return nil
		})
	}
	return err
}

func wasRemoved(id string, removedType int) (wasRemoved bool) {
	DB.View(func(tx *bolt.Tx) error {
		c := tx.Bucket([]byte(bucket)).Cursor()
		prefix := []byte(fmt.Sprintf("%d_", removedType))
		for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			var item *DBItem
			if err := json.Unmarshal(v, &item); err != nil {
				return err
			}
			if item.ID == id {
				wasRemoved = true
				return nil
			}
		}
		return nil
	})
	return wasRemoved
}

//
// Library updates
//
func doUpdateLibrary() error {
	if err := checkShowsPath(); err != nil {
		return err
	}

	DB.View(func(tx *bolt.Tx) error {
		c := tx.Bucket([]byte(bucket)).Cursor()
		prefix := []byte(fmt.Sprintf("%d_", Show))
		for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			var item *DBItem
			if err := json.Unmarshal(v, &item); err != nil {
				return err
			}
			if _, err := writeShowStrm(item.ID); err != nil {
				libraryLog.Error(err)
			}
		}
		return nil
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
func syncMoviesList(listId string, updating bool) (err error) {
	if err := checkMoviesPath(); err != nil {
		return err
	}

	var label string
	var movies []*trakt.Movies

	switch listId {
	case "watchlist":
		movies, err = trakt.WatchlistMovies()
		label = "LOCALIZE[30254]"
	case "collection":
		movies, err = trakt.CollectionMovies()
		label = "LOCALIZE[30257]"
	default:
		movies, err = trakt.ListItemsMovies(listId, "0")
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

		tmdbId := strconv.Itoa(movie.Movie.IDs.TMDB)

		if updating && wasRemoved(tmdbId, RemovedMovie) {
			continue
		}

		if err := isDuplicateMovie(tmdbId); err != nil {
			if !updating {
				libraryLog.Warning(err)
			}
			continue
		}

		if _, err := writeMovieStrm(tmdbId); err != nil {
			libraryLog.Error(err)
			continue
		}

		movieIDs = append(movieIDs, tmdbId)
	}

	if err := updateDB(Batch, Movie, movieIDs, 0); err != nil {
		return err
	}

	if !updating {
		libraryLog.Noticef("Movies list (%s) added", listId)
		if xbmc.DialogConfirm("Quasar", fmt.Sprintf("LOCALIZE[30277];;%s", label)) {
			xbmc.VideoLibraryScan()
		}
	}
	return nil
}

func writeMovieStrm(tmdbId string) (*tmdb.Movie, error) {
	movie := tmdb.GetMovieById(tmdbId, "en")
	if movie == nil {
		return movie, errors.New(fmt.Sprintf("Unable to get movie (%s)", tmdbId))
	}

	movieStrm := toFileName(fmt.Sprintf("%s (%s)", movie.OriginalTitle, strings.Split(movie.ReleaseDate, "-")[0]))
	moviePath := filepath.Join(moviesLibraryPath, movieStrm)

	if _, err := os.Stat(moviePath); os.IsNotExist(err) {
		if err := os.Mkdir(moviePath, 0755); err != nil {
			libraryLog.Error(err)
			return movie, err
		}
	}

	movieStrmPath := filepath.Join(moviePath, fmt.Sprintf("%s.strm", movieStrm))

	playLink := UrlForXBMC("/movie/%s/play", tmdbId)
	if config.Get().ChooseStreamAuto == false {
		playLink = strings.Replace(playLink, "/play", "/links", 1)
	}
	if err := ioutil.WriteFile(movieStrmPath, []byte(playLink), 0644); err != nil {
		libraryLog.Error(err)
		return movie, err
	}

	return movie, nil
}

func removeMovie(tmdbId string) error {
	if err := checkMoviesPath(); err != nil {
		return err
	}
	movie := tmdb.GetMovieById(tmdbId, "en")
	movieName := fmt.Sprintf("%s (%s)", movie.OriginalTitle, strings.Split(movie.ReleaseDate, "-")[0])
	movieStrm := toFileName(movieName)
	moviePath := filepath.Join(moviesLibraryPath, movieStrm)

	if _, err := os.Stat(moviePath); err != nil {
		return errors.New("Nothing left to remove from Quasar")
	}
	if err := os.RemoveAll(moviePath); err != nil {
		return err
	}

	if err := updateDB(Delete, Movie, []string{tmdbId}, 0); err != nil {
		return err
	}
	if err := updateDB(Update, RemovedMovie, []string{tmdbId}, 0); err != nil {
		return err
	}

	libraryLog.Warningf("%s removed from library", movieName)
	if xbmc.DialogConfirm("Quasar", fmt.Sprintf("LOCALIZE[30278];;%s", movieName)) {
		xbmc.VideoLibraryClean()
	}

	return nil
}

//
// Shows internals
//
func syncShowsList(listId string, updating bool) (err error) {
	if err := checkShowsPath(); err != nil {
		return err
	}

	var label string
	var shows []*trakt.Shows

	switch listId {
	case "watchlist":
		shows, err = trakt.WatchlistShows()
		label = "LOCALIZE[30254]"
	case "collection":
		shows, err = trakt.CollectionShows()
		label = "LOCALIZE[30257]"
	default:
		shows, err = trakt.ListItemsShows(listId, "0")
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

		tmdbId := strconv.Itoa(show.Show.IDs.TMDB)

		if updating && wasRemoved(tmdbId, RemovedShow) {
			continue
		}

		if !updating {
			if err := isDuplicateShow(tmdbId); err != nil {
				libraryLog.Warning(err)
				continue
			}
		}

		if _, err := writeShowStrm(tmdbId); err != nil {
			libraryLog.Error(err)
			continue
		}

		showIDs = append(showIDs, tmdbId)
	}

	if err := updateDB(Batch, Show, showIDs, 0); err != nil {
		return err
	}

	if !updating {
		libraryLog.Noticef("Shows list (%s) added", listId)
		if xbmc.DialogConfirm("Quasar", fmt.Sprintf("LOCALIZE[30277];;%s", label)) {
			xbmc.VideoLibraryScan()
		}
	}
	return nil
}

func writeShowStrm(showId string) (*tmdb.Show, error) {
	Id, _ := strconv.Atoi(showId)
	show := tmdb.GetShow(Id, "en")
	if show == nil {
		return nil, errors.New(fmt.Sprintf("Unable to get show (%s)", showId))
	}
	showStrm := toFileName(fmt.Sprintf("%s (%s)", show.Name, strings.Split(show.FirstAirDate, "-")[0]))
	showPath := filepath.Join(showsLibraryPath, showStrm)
	playSuffix := "play"
	if config.Get().ChooseStreamAuto == false {
		playSuffix = "links"
	}

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

		episodes := tmdb.GetSeason(Id, season.Season, "en").Episodes

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

			// TODO Check if single episode was previously removed?

			if err := isDuplicateEpisode(Id, season.Season, episode.EpisodeNumber); err != nil {
				continue
			}

			episodeStrmPath := filepath.Join(showPath, fmt.Sprintf("%s S%02dE%02d.strm", showStrm, season.Season, episode.EpisodeNumber))
			playLink := UrlForXBMC("/show/%d/season/%d/episode/%d/%s", Id, season.Season, episode.EpisodeNumber, playSuffix)
			if err := ioutil.WriteFile(episodeStrmPath, []byte(playLink), 0644); err != nil {
				libraryLog.Error(err)
				return show, err
			}
		}
	}

	return show, nil
}

func removeEpisode(tmdbId string, showId string, seasonNumber int, episodeNumber int) error {
	if err := checkShowsPath(); err != nil {
		return err
	}
	Id, _ := strconv.Atoi(showId)
	show := tmdb.GetShow(Id, "en")

	if show == nil {
		return errors.New("Unable to find show to remove episode")
	}

	showPath := toFileName(fmt.Sprintf("%s (%s)", show.Name, strings.Split(show.FirstAirDate, "-")[0]))
	episodeStrm := fmt.Sprintf("%s S%02dE%02d.strm", showPath, seasonNumber, episodeNumber)
	episodePath := filepath.Join(showsLibraryPath, showPath, episodeStrm)

	if _, err := os.Stat(episodePath); err != nil {
		return errors.New("Nothing left to remove from Quasar")
	}
	if err := os.Remove(episodePath); err != nil {
		return err
	}

	// TODO Add single episodes to removed prefix?
	// if err := updateDB(Update, RemovedEpisode, []string{tmdbId}, Id); err != nil {
	// 	return err
	// }

	libraryLog.Noticef("%s removed from library", episodeStrm)
	if xbmc.DialogConfirm("Quasar", fmt.Sprintf("LOCALIZE[30278];;%s S%02dE%02d", show.Name, seasonNumber, episodeNumber)) {
		xbmc.VideoLibraryClean()
	}

	return nil
}

//
// Movie externals
//
func AddMovie(ctx *gin.Context) {
	if err := checkMoviesPath(); err != nil {
		ctx.String(200, err.Error())
		return
	}
	tmdbId := ctx.Params.ByName("tmdbId")

	if err := isDuplicateMovie(tmdbId); err != nil {
		libraryLog.Warningf(err.Error())
		xbmc.Notify("Quasar", "LOCALIZE[30265]", config.AddonIcon())
		return
	}

	var err error
	var movie *tmdb.Movie
	if movie, err = writeMovieStrm(tmdbId); err != nil {
		ctx.String(200, err.Error())
		return
	}

	if err := updateDB(Update, Movie, []string{tmdbId}, 0); err != nil {
		ctx.String(200, err.Error())
		return
	}
	if err := updateDB(Delete, RemovedMovie, []string{tmdbId}, 0); err != nil {
		ctx.String(200, err.Error())
		return
	}

	libraryLog.Noticef("%s added to library", movie.Title)
	if xbmc.DialogConfirm("Quasar", fmt.Sprintf("LOCALIZE[30277];;%s", movie.Title)) {
		xbmc.VideoLibraryScan()
	}
}

func AddMoviesList(ctx *gin.Context) {
	listId := ctx.Params.ByName("listId")
	updatingStr := ctx.DefaultQuery("updating", "false")

	updating := false
	if updatingStr != "false" {
		updating = true
	}

	syncMoviesList(listId, updating)
}

func RemoveMovie(ctx *gin.Context) {
	tmdbId := ctx.Params.ByName("tmdbId")
	if err := removeMovie(tmdbId); err != nil {
		ctx.String(200, err.Error())
	}
}

//
// Shows externals
//
func AddShow(ctx *gin.Context) {
	if err := checkShowsPath(); err != nil {
		ctx.String(200, err.Error())
		return
	}
	tmdbId := ctx.Params.ByName("tmdbId")
	merge := ctx.DefaultQuery("merge", "false")

	label := "LOCALIZE[30277]"
	logMsg := "%s (%s) added to library"
	if merge == "false" {
		if err := isDuplicateShow(tmdbId); err != nil {
			libraryLog.Warning(err)
			xbmc.Notify("Quasar", "LOCALIZE[30265]", config.AddonIcon())
			return
		}
	} else {
		label = "LOCALIZE[30286]"
		logMsg = "%s (%s) merged to library"
	}

	var err error
	var show *tmdb.Show
	if show, err = writeShowStrm(tmdbId); err != nil {
		libraryLog.Error(err)
		ctx.String(200, err.Error())
		return
	}

	if err := updateDB(Update, Show, []string{tmdbId}, 0); err != nil {
		ctx.String(200, err.Error())
		return
	}
	if err := updateDB(Delete, RemovedShow, []string{tmdbId}, 0); err != nil {
		ctx.String(200, err.Error())
		return
	}

	libraryLog.Noticef(logMsg, show.Name, tmdbId)
	if xbmc.DialogConfirm("Quasar", fmt.Sprintf("%s;;%s", label, show.Name)) {
		xbmc.VideoLibraryScan()
	}
}

func AddShowsList(ctx *gin.Context) {
	listId := ctx.Params.ByName("listId")
	updatingStr := ctx.DefaultQuery("updating", "false")

	updating := false
	if updatingStr != "false" {
		updating = true
	}

	syncShowsList(listId, updating)
}

func RemoveShow(ctx *gin.Context) {
	if err := checkShowsPath(); err != nil {
		ctx.String(200, err.Error())
		return
	}
	tmdbId := ctx.Params.ByName("tmdbId")
	Id, _ := strconv.Atoi(tmdbId)
	show := tmdb.GetShow(Id, "en")

	if show == nil {
		ctx.String(200, "Unable to find show to remove")
		return
	}

	showStrm := toFileName(fmt.Sprintf("%s (%s)", show.Name, strings.Split(show.FirstAirDate, "-")[0]))
	showPath := filepath.Join(showsLibraryPath, showStrm)

	if _, err := os.Stat(showPath); os.IsNotExist(err) {
		libraryLog.Warning(err)
		ctx.String(200, "LOCALIZE[30282]")
		return
	}
	if err := os.RemoveAll(showPath); err != nil {
		libraryLog.Error(err)
		ctx.String(200, err.Error())
		return
	}

	if err := updateDB(Delete, Show, []string{tmdbId}, 0); err != nil {
		ctx.String(200, err.Error())
		return
	}
	if err := updateDB(Update, RemovedShow, []string{tmdbId}, 0); err != nil {
		ctx.String(200, err.Error())
		return
	}

	libraryLog.Noticef("%s removed from library", show.Name)
	if xbmc.DialogConfirm("Quasar", fmt.Sprintf("LOCALIZE[30278];;%s", show.Name)) {
		xbmc.VideoLibraryClean()
	}
}

//
// Library update loop
//
func LibraryUpdateLoop() {
	if err := checkMoviesPath(); err != nil {
		xbmc.Notify("Quasar", err.Error(), config.AddonIcon())
		return
	}
	if err := checkShowsPath(); err != nil {
		xbmc.Notify("Quasar", err.Error(), config.AddonIcon())
		return
	}

	db, err := bolt.Open(filepath.Join(config.Get().Info.Profile, "library.db"), 0600, &bolt.Options{
		ReadOnly: false,
		Timeout: 15 * time.Second,
	})
	if err != nil {
		libraryLog.Error(err)
		return
	}
	defer db.Close()

	DB = db

	db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(bucket))
		if err != nil {
			libraryLog.Error(err)
			xbmc.Notify("Quasar", err.Error(), config.AddonIcon())
		  return err
	  }
		return nil
	})

	// Migrate old QuasarDB.json
	if _, err := os.Stat(filepath.Join(libraryPath, "QuasarDB.json")); err == nil {
		libraryLog.Warning("Found QuasarDB.json, upgrading to BoltDB...")
		var oldDB struct {
			Movies []string `json:"movies"`
			Shows  []string `json:"shows"`
		}
		oldFile := filepath.Join(libraryPath, "QuasarDB.json")
		file, err := ioutil.ReadFile(oldFile)
		if err != nil {
			xbmc.Notify("Quasar", err.Error(), config.AddonIcon())
		} else {
			if err := json.Unmarshal(file, &oldDB); err != nil {
				xbmc.Notify("Quasar", err.Error(), config.AddonIcon())
			} else if err := updateDB(Batch, Movie, oldDB.Movies, 0); err != nil {
				xbmc.Notify("Quasar", err.Error(), config.AddonIcon())
			} else if err := updateDB(Batch, Show, oldDB.Shows, 0); err != nil {
				xbmc.Notify("Quasar", err.Error(), config.AddonIcon())
			} else {
				os.Remove(oldFile)
				libraryLog.Notice("Successfully imported and removed QuasarDB.json")
			}
		}
	}

	go func() {
		// Give time to Kodi to start its JSON-RPC service
		time.Sleep(5 * time.Second)
		updateLibraryMovies()
		updateLibraryShows()
	}()

	updateDelay := config.Get().UpdateDelay
	if updateDelay > 0 {
		if updateDelay < 10 {
			// Give time to Quasar to update its cache of libraryMovies, libraryShows and libraryEpisodes
			updateDelay = 10
		}
		go func() {
			time.Sleep(time.Duration(updateDelay) * time.Second)
			select {
			case <- closing:
				return
			default:
				if err := doUpdateLibrary(); err != nil {
					libraryLog.Warning(err)
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

	for {
		select {
		case <- updateTicker.C:
			if config.Get().UpdateFrequency > 0 {
				if err := doUpdateLibrary(); err != nil {
					libraryLog.Warning(err)
				}
			}
		case <- traktSyncTicker.C:
			if config.Get().TraktSyncFrequency > 0 {
				if err := doSyncTrakt(); err != nil {
					libraryLog.Warning(err)
				}
			}
		case <- closing:
			libraryLog.Info("Closing library...")
			return
		}
	}
}

func UpdateLibrary(ctx *gin.Context) {
	if err := doUpdateLibrary(); err != nil {
		ctx.String(200, err.Error())
	}
}

func CloseLibrary() {
	close(closing)
}

//
// Kodi notifications
//
func Notification(ctx *gin.Context) {
	sender := ctx.Query("sender")
	method := ctx.Query("method")
	data := ctx.Query("data")

	if sender == "xbmc" {
		switch method {
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
				for _, episodes := range libraryEpisodes {
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
				var showId string
				for _, tvshow := range libraryShows.Shows {
					if tvshow.ID == episode.TVShowID {
						showId = tvshow.IMDBNumber
						break
					}
				}
				if showId != "" && episode.UniqueIDs.ID != "" {
					if err := removeEpisode(episode.UniqueIDs.ID, showId, episode.Season, episode.Episode); err != nil {
						libraryLog.Warning(err)
					}
				} else {
					libraryLog.Warning("Missing episodeid or tvshowid, nothing to remove")
				}
			case "movie":
				for _, movie := range libraryMovies.Movies {
					if movie.ID == item.ID {
						tmdbMovie := tmdb.GetMovieById(movie.IMDBNumber, "en")
						if tmdbMovie == nil || tmdbMovie.Id == 0 {
							break
						}
						if err := removeMovie(strconv.Itoa(tmdbMovie.Id)); err != nil {
							libraryLog.Warning(err.Error())
							break
						}
					}
				}
			}
		case "VideoLibrary.OnScanFinished":
			fallthrough
		case "VideoLibrary.OnCleanFinished":
			updateLibraryMovies()
			updateLibraryShows()
			clearPageCache(ctx)
		}
	}
}

// DEPRECATED
func PlayMovie(btService *bittorrent.BTService) gin.HandlerFunc {
	if config.Get().ChooseStreamAuto == true {
		return MoviePlay(btService)
	} else {
		return MovieLinks(btService)
	}
}
func PlayShow(btService *bittorrent.BTService) gin.HandlerFunc {
	if config.Get().ChooseStreamAuto == true {
		return ShowEpisodePlay(btService)
	} else {
		return ShowEpisodeLinks(btService)
	}
}
