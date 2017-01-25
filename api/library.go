package api

import (
	"os"
	"fmt"
	"time"
	"errors"
	"strconv"
	"strings"
	"io/ioutil"
	"path/filepath"

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
	TVDBScraper = iota
	TMDBScraper
	TraktScraper
)

var (
	libraryLog        = logging.MustGetLogger("library")
	LibraryMovies     = xbmc.VideoLibraryGetMovies()
	LibraryShows      = xbmc.VideoLibraryGetShows()
	LibraryEpisodes   = make(map[int]*xbmc.VideoLibraryEpisodes)
	LibraryPath       string
	MoviesLibraryPath string
	ShowsLibraryPath  string
)

type DataBase struct {
	Movies []string `json:"movies"`
	Shows  []string `json:"shows"`
}

type Item struct {
	Id       string `json:"id"`
	Title    string `json:"title"`
	Year     string `json:"year"`
	Overview string `json:"overview"`
	Poster   string `json:"poster"`
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

func checkLibraryPath() error {
	if LibraryPath == "" {
		LibraryPath = config.Get().LibraryPath
	}
	if fileInfo, err := os.Stat(LibraryPath); err != nil || fileInfo.IsDir() == false || LibraryPath == "" || LibraryPath == "." {
		xbmc.Notify("Quasar", "LOCALIZE[30220]", config.AddonIcon())
		return err
	}
	return nil
}
func checkMoviesPath() error {
	if err := checkLibraryPath(); err != nil {
		return err
	}
	if MoviesLibraryPath == "" {
		MoviesLibraryPath = filepath.Join(LibraryPath, "Movies")
	}
	if _, err := os.Stat(MoviesLibraryPath); os.IsNotExist(err) {
		if err := os.Mkdir(MoviesLibraryPath, 0755); err != nil {
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
	if ShowsLibraryPath == "" {
		ShowsLibraryPath = filepath.Join(LibraryPath, "Shows")
	}
	if _, err := os.Stat(ShowsLibraryPath); os.IsNotExist(err) {
		if err := os.Mkdir(ShowsLibraryPath, 0755); err != nil {
			libraryLog.Error(err)
			return err
		}
	}
	return nil
}

func updateLibraryMovies() {
	LibraryMovies = xbmc.VideoLibraryGetMovies()
}
func updateLibraryShows() {
	LibraryShows = xbmc.VideoLibraryGetShows()
}
func updateLibraryEpisodes(showId int) {
	LibraryEpisodes[showId] = xbmc.VideoLibraryGetEpisodes(showId)
}

func isDuplicateMovie(tmdbId string) error {
	movie := tmdb.GetMovieById(tmdbId, "en")
	if movie == nil || movie.IMDBId == "" {
		return nil
	}
	for _, existingMovie := range LibraryMovies.Movies {
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
		showId = strconv.Itoa(util.StrInterfaceToInt(show.ExternalIDs.TVDBID))
	case TraktScraper:
		traktShow := trakt.GetShowByTMDB(tmdbId)
		showId = strconv.Itoa(traktShow.IDs.Trakt)
	}
	for _, existingShow := range LibraryShows.Shows {
		// TODO Aho-Corasick name matching to allow mixed scraper sources
		if existingShow.IMDBNumber == showId {
			return errors.New(fmt.Sprintf("%s already in library", show.Name))
		}
	}
	return nil
}
func isDuplicateEpisode(showId int, seasonNumber int, episodeNumber int) error {
	episode := tmdb.GetEpisode(showId, seasonNumber, episodeNumber, "en")
	if episode == nil || episode.ExternalIDs == nil {
		libraryLog.Warning("No external IDs found")
		return nil
	}
	episodeId := strconv.Itoa(episode.Id)
	switch config.Get().TvScraper {
	case TMDBScraper:
		break
	case TVDBScraper:
		episodeId = strconv.Itoa(util.StrInterfaceToInt(episode.ExternalIDs.TVDBID))
	// case TraktScraper: // TODO
	// 	traktShow := trakt.GetEpisodeByTMDB(episodeId)
	// 	episodeId = strconv.Itoa(traktShow.IDs.Trakt)
	}
	var tvshowId int
	tvshowIMDBId := strconv.Itoa(showId)
	for _, tvshow := range LibraryShows.Shows {
		if tvshow.IMDBNumber == tvshowIMDBId {
			tvshowId = tvshow.ID
			break
		}
	}
	if tvshowId == 0 {
		libraryLog.Warningf("No matching tvshowid for %s (S%02dE%02d)", episode.Name, seasonNumber, episodeNumber)
		return nil
	}
	var existingEpisodes *xbmc.VideoLibraryEpisodes
	if _, exists := LibraryEpisodes[showId]; exists {
		existingEpisodes = LibraryEpisodes[showId]
	} else {
		existingEpisodes = xbmc.VideoLibraryGetEpisodes(tvshowId)
		LibraryEpisodes[showId] = existingEpisodes
	}
	// if len(existingEpisodes.Episodes) == 0 {
	// 	libraryLog.Warningf("No episodes in library for %s (S%02dE%02d)", episode.Name, seasonNumber, episodeNumber)
	// 	return nil
	// }
	for _, existingEpisode := range existingEpisodes.Episodes {
		if existingEpisode.UniqueIDs.ID == episodeId ||
			 (existingEpisode.Season == seasonNumber && existingEpisode.Episode == episodeNumber) {
			return errors.New(fmt.Sprintf("S%02dE%02d already in library", seasonNumber, episodeNumber))
		}
	}
	return nil
}

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

	libraryLog.Noticef("%s added to library", movie.Title)
	if xbmc.DialogConfirm("Quasar", fmt.Sprintf("LOCALIZE[30277];;%s", movie.Title)) {
		if ret := xbmc.VideoLibraryScan(); ret == "OK" {
			libraryLog.Info("Scan returned", ret)
			// updateLibraryMovies() // FIXME those two are fine with Clean, but error out on Scan...
			// clearPageCache(ctx)
		} else {
			libraryLog.Warning("Scan returned", ret)
		}
	}
}

func AddMovieList(ctx *gin.Context) {
	if err := checkMoviesPath(); err != nil {
		ctx.String(200, err.Error())
		return
	}
	listId := ctx.Params.ByName("listId")
	updating := ctx.DefaultQuery("updating", "false")

	movies, err := trakt.ListItemsMovies(listId, "0")
	if err != nil {
		libraryLog.Error(err)
		ctx.String(200, err.Error())
		return
	}

	for _, movie := range movies {
		title := movie.Movie.Title
		if movie.Movie.IDs.TMDB == 0 {
			libraryLog.Warningf("Missing TMDB ID for %s", title)
			continue
		}

		tmdbId := strconv.Itoa(movie.Movie.IDs.TMDB)

		if err := isDuplicateMovie(tmdbId); err != nil {
			libraryLog.Warning(err)
			continue
		}

		if _, err := writeMovieStrm(tmdbId); err != nil {
			libraryLog.Error(err)
			continue
		}
	}

	if updating == "false" {
		xbmc.Notify("Quasar", "LOCALIZE[30281]", config.AddonIcon())
	}
	libraryLog.Noticef("Movie list #%s added", listId)
}

func AddMovieCollection(ctx *gin.Context) {
	if err := checkMoviesPath(); err != nil {
		ctx.String(200, err.Error())
		return
	}
	updating := ctx.DefaultQuery("updating", "false")

	movies, err := trakt.CollectionMovies()
	if err != nil {
		libraryLog.Error(err)
		ctx.String(200, err.Error())
		return
	}

	for _, movie := range movies {
		title := movie.Movie.Title
		if movie.Movie.IDs.TMDB == 0 {
			libraryLog.Warningf("Missing TMDB ID for %s", title)
			continue
		}
		tmdbId := strconv.Itoa(movie.Movie.IDs.TMDB)
		if err := isDuplicateMovie(movie.Movie.IDs.IMDB); err != nil {
			libraryLog.Warning(err)
			continue
		}
		if _, err := writeMovieStrm(tmdbId); err != nil {
			libraryLog.Error(err)
			continue
		}
	}

	if updating == "false" {
		xbmc.Notify("Quasar", "LOCALIZE[30280]", config.AddonIcon())
	}
	libraryLog.Notice("Movie collection added")
}

func AddMovieWatchlist(ctx *gin.Context) {
	if err := checkMoviesPath(); err != nil {
		ctx.String(200, err.Error())
		return
	}
	updating := ctx.DefaultQuery("updating", "false")

	movies, err := trakt.WatchlistMovies()
	if err != nil {
		libraryLog.Error(err)
		ctx.String(200, err.Error())
		return
	}

	for _, movie := range movies {
		title := movie.Movie.Title
		if movie.Movie.IDs.TMDB == 0 {
			libraryLog.Warningf("Missing TMDB ID for %s", title)
			continue
		}
		tmdbId := strconv.Itoa(movie.Movie.IDs.TMDB)
		if err := isDuplicateMovie(movie.Movie.IDs.IMDB); err != nil {
			libraryLog.Warning(err)
			continue
		}
		if _, err := writeMovieStrm(tmdbId); err != nil {
			libraryLog.Error(err)
			continue
		}
	}

	if updating == "false" {
		xbmc.Notify("Quasar", "LOCALIZE[30279]", config.AddonIcon())
	}
	libraryLog.Notice("Movie watchlist added")
}

func writeMovieStrm(tmdbId string) (*tmdb.Movie, error) {
	movie := tmdb.GetMovieById(tmdbId, "en")
	if movie == nil {
		return movie, errors.New("Unable to get movie")
	}

	movieStrm := toFileName(fmt.Sprintf("%s (%s)", movie.OriginalTitle, strings.Split(movie.ReleaseDate, "-")[0]))
	moviePath := filepath.Join(MoviesLibraryPath, movieStrm)

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

func RemoveMovie(ctx *gin.Context) {
	if err := checkMoviesPath(); err != nil {
		ctx.String(200, err.Error())
		return
	}
	tmdbId := ctx.Params.ByName("tmdbId")
	movie := tmdb.GetMovieById(tmdbId, "en")
	movieName := fmt.Sprintf("%s (%s)", movie.OriginalTitle, strings.Split(movie.ReleaseDate, "-")[0])
	movieStrm := toFileName(movieName)
	moviePath := filepath.Join(MoviesLibraryPath, movieStrm)

	if _, err := os.Stat(moviePath); err != nil {
		libraryLog.Error(err)
		ctx.String(200, "LOCALIZE[30282]")
		return
	}
	if err := os.RemoveAll(moviePath); err != nil {
		ctx.String(200, err.Error())
		return
	}

	// xbmc.Notify("Quasar", "LOCALIZE[30222]", config.AddonIcon())
	libraryLog.Noticef("%s removed from library", movieName)
	if xbmc.DialogConfirm("Quasar", fmt.Sprintf("LOCALIZE[30278];;%s", movieName)) {
		clearPageCache(ctx)
		if ret := xbmc.VideoLibraryClean(); ret == "" {
			libraryLog.Info("Clean returned", ret)
			updateLibraryMovies()
		} else {
			libraryLog.Warning("Clean returned", ret)
		}
	}
}

func AddShow(ctx *gin.Context) {
	if err := checkShowsPath(); err != nil {
		ctx.String(200, err.Error())
		return
	}
	tmdbId := ctx.Params.ByName("tmdbId")

	if err := isDuplicateShow(tmdbId); err != nil {
		libraryLog.Warning(err)
		xbmc.Notify("Quasar", "LOCALIZE[30265]", config.AddonIcon())
		return
	}

	var err error
	var show *tmdb.Show
	if show, err = writeShowStrm(tmdbId, false); err != nil {
		libraryLog.Error(err)
		ctx.String(200, err.Error())
		return
	}

	libraryLog.Noticef("%s added to library", show.Name)
	if xbmc.DialogConfirm("Quasar", fmt.Sprintf("LOCALIZE[30277];;%s", show.Name)) {
		if ret := xbmc.VideoLibraryScan(); ret == "OK" {
			libraryLog.Info("Scan returned", ret)
			// updateLibraryShows() // FIXME see above
			// clearPageCache(ctx)
		} else {
			libraryLog.Warning("Scan returned", ret)
		}
	}
}

func MergeShow(ctx *gin.Context) {
	if err := checkShowsPath(); err != nil {
		ctx.String(200, err.Error())
		return
	}
	tmdbId := ctx.Params.ByName("tmdbId")

	var err error
	var show *tmdb.Show
	if show, err = writeShowStrm(tmdbId, true); err != nil {
		libraryLog.Error(err)
		ctx.String(200, err.Error())
		return
	}

	libraryLog.Noticef("%s (%s) merged to library", show.Name, tmdbId)
	if xbmc.DialogConfirm("Quasar", fmt.Sprintf("LOCALIZE[30286];;%s", show.Name)) {
		if ret := xbmc.VideoLibraryScan(); ret == "OK" {
			libraryLog.Info("Scan returned", ret)
			// updateLibraryShows() // FIXME see above
			// clearPageCache(ctx)
		} else {
			libraryLog.Warning("Scan returned", ret)
		}
	}
}

func AddShowList(ctx *gin.Context) {
	if err := checkShowsPath(); err != nil {
		ctx.String(200, err.Error())
		return
	}
	listId := ctx.Params.ByName("listId")
	merging := ctx.DefaultQuery("merge", "false")
	updating := ctx.DefaultQuery("updating", "false")
	var merge bool

	if merging != "false" || updating != "false" {
		merge = true
	}

	shows, err := trakt.ListItemsShows(listId, "0")
	if err != nil {
		libraryLog.Error(err)
		ctx.String(200, err.Error())
		return
	}

	for _, show := range shows {
		title := show.Show.Title
		if show.Show.IDs.TMDB == 0 {
			libraryLog.Warningf("Missing TMDB ID for %s", title)
			continue
		}
		tmdbId := strconv.Itoa(show.Show.IDs.TMDB)
		if !merge {
			if err := isDuplicateShow(tmdbId); err != nil {
				libraryLog.Warning(err)
				continue
			}
		}
		if _, err := writeShowStrm(tmdbId, merge); err != nil {
			libraryLog.Error(err)
			continue
		}
	}

	if updating == "false" {
		xbmc.Notify("Quasar", "LOCALIZE[30281]", config.AddonIcon())
	}
	libraryLog.Noticef("Show list #%s added", listId)
}

func AddShowCollection(ctx *gin.Context) {
	if err := checkShowsPath(); err != nil {
		ctx.String(200, err.Error())
		return
	}
	merging := ctx.DefaultQuery("merge", "false")
	updating := ctx.DefaultQuery("updating", "false")
	var merge bool

	if merging != "false" || updating != "false" {
		merge = true
	}

	shows, err := trakt.CollectionShows()
	if err != nil {
		libraryLog.Error(err)
		ctx.String(200, err.Error())
		return
	}

	for _, show := range shows {
		title := show.Show.Title
		if show.Show.IDs.TMDB == 0 {
			libraryLog.Warningf("Missing TMDB ID for %s", title)
			continue
		}
		tmdbId := strconv.Itoa(show.Show.IDs.TMDB)
		if !merge {
			if err := isDuplicateShow(tmdbId); err != nil {
				libraryLog.Warning(err)
				continue
			}
		}
		if _, err := writeShowStrm(tmdbId, merge); err != nil {
			libraryLog.Error(err)
			continue
		}
	}

	if updating == "false" {
		xbmc.Notify("Quasar", "LOCALIZE[30280]", config.AddonIcon())
	}
	libraryLog.Notice("Show collection added")
}

func AddShowWatchlist(ctx *gin.Context) {
	if err := checkShowsPath(); err != nil {
		ctx.String(200, err.Error())
		return
	}
	merging := ctx.DefaultQuery("merge", "false")
	updating := ctx.DefaultQuery("updating", "false")
	var merge bool

	if merging != "false" || updating != "false" {
		merge = true
	}

	shows, err := trakt.WatchlistShows()
	if err != nil {
		libraryLog.Error(err)
		ctx.String(200, err.Error())
		return
	}

	for _, show := range shows {
		title := show.Show.Title
		if show.Show.IDs.TMDB == 0 {
			libraryLog.Warningf("Missing TMDB ID for %s", title)
			continue
		}
		tmdbId := strconv.Itoa(show.Show.IDs.TMDB)
		if !merge {
			if err := isDuplicateShow(tmdbId); err != nil {
				libraryLog.Warning(err)
				continue
			}
		}
		if _, err := writeShowStrm(tmdbId, merge); err != nil {
			libraryLog.Error(err)
			continue
		}
	}

	if updating == "false" {
		xbmc.Notify("Quasar", "LOCALIZE[30279]", config.AddonIcon())
	}
	libraryLog.Notice("Show watchlist added")
}

func writeShowStrm(showId string, merge bool) (*tmdb.Show, error) {
	Id, _ := strconv.Atoi(showId)
	show := tmdb.GetShow(Id, "en")
	if show == nil {
		return nil, errors.New("Unable to get Show")
	}
	showStrm := toFileName(fmt.Sprintf("%s (%s)", show.Name, strings.Split(show.FirstAirDate, "-")[0]))
	showPath := filepath.Join(ShowsLibraryPath, showStrm)

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
			if err := isDuplicateEpisode(Id, season.Season, episode.EpisodeNumber); err != nil {
				libraryLog.Warning(err)
				continue
			}
			episodeStrmPath := filepath.Join(showPath, fmt.Sprintf("%s S%02dE%02d.strm", showStrm, season.Season, episode.EpisodeNumber))
			playLink := UrlForXBMC("/show/%d/season/%d/episode/%d/play", Id, season.Season, episode.EpisodeNumber)
			if config.Get().ChooseStreamAuto == false {
				playLink = strings.Replace(playLink, "/play", "/links", 1)
			}
			if err := ioutil.WriteFile(episodeStrmPath, []byte(playLink), 0644); err != nil {
				libraryLog.Error(err)
				return show, err
			}
		}
	}

	return show, nil
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
	showPath := filepath.Join(ShowsLibraryPath, showStrm)

	if _, err := os.Stat(showPath); os.IsNotExist(err) {
		libraryLog.Error(err)
		ctx.String(200, "LOCALIZE[30282]")
		return
	}
	if err := os.RemoveAll(showPath); err != nil {
		libraryLog.Error(err)
		ctx.String(200, err.Error())
		return
	}

	libraryLog.Noticef("%s removed from library", show.Name)
	if xbmc.DialogConfirm("Quasar", fmt.Sprintf("LOCALIZE[30278];;%s", show.Name)) {
		clearPageCache(ctx)
		if ret := xbmc.VideoLibraryClean(); ret == "" {
			libraryLog.Info("Clean returned", ret)
			updateLibraryShows()
		} else {
			libraryLog.Warning("Clean returned", ret)
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
