package api

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"time"

	"github.com/elgatito/elementum/cache"
	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/trakt"
	"github.com/elgatito/elementum/xbmc"
	"github.com/gin-gonic/gin"
)

func inMoviesWatchlist(tmdbID int) bool {
	if config.Get().TraktToken == "" {
		return false
	}

	var movies []*trakt.Movies

	cacheStore := cache.NewFileStore(path.Join(config.Get().ProfilePath, "cache"))
	key := fmt.Sprintf("com.trakt.watchlist.movies")
	if err := cacheStore.Get(key, &movies); err != nil {
		movies, _ = trakt.WatchlistMovies()
		cacheStore.Set(key, movies, 30*time.Second)
	}

	for _, movie := range movies {
		if tmdbID == movie.Movie.IDs.TMDB {
			return true
		}
	}
	return false
}

func inShowsWatchlist(tmdbID int) bool {
	if config.Get().TraktToken == "" {
		return false
	}

	var shows []*trakt.Shows

	cacheStore := cache.NewFileStore(path.Join(config.Get().ProfilePath, "cache"))
	key := fmt.Sprintf("com.trakt.watchlist.shows")
	if err := cacheStore.Get(key, &shows); err != nil {
		shows, _ = trakt.WatchlistShows()
		cacheStore.Set(key, shows, 30*time.Second)
	}

	for _, show := range shows {
		if tmdbID == show.Show.IDs.TMDB {
			return true
		}
	}
	return false
}

func inMoviesCollection(tmdbID int) bool {
	if config.Get().TraktToken == "" {
		return false
	}

	var movies []*trakt.Movies

	cacheStore := cache.NewFileStore(path.Join(config.Get().ProfilePath, "cache"))
	key := fmt.Sprintf("com.trakt.collection.movies")
	if err := cacheStore.Get(key, &movies); err != nil {
		movies, _ = trakt.CollectionMovies()
		cacheStore.Set(key, movies, 30*time.Second)
	}

	for _, movie := range movies {
		if tmdbID == movie.Movie.IDs.TMDB {
			return true
		}
	}
	return false
}

func inShowsCollection(tmdbID int) bool {
	if config.Get().TraktToken == "" {
		return false
	}

	var shows []*trakt.Shows

	cacheStore := cache.NewFileStore(path.Join(config.Get().ProfilePath, "cache"))
	key := fmt.Sprintf("com.trakt.collection.shows")
	if err := cacheStore.Get(key, &shows); err != nil {
		shows, _ = trakt.CollectionShows()
		cacheStore.Set(key, shows, 30*time.Second)
	}

	for _, show := range shows {
		if tmdbID == show.Show.IDs.TMDB {
			return true
		}
	}
	return false
}

//
// Authorization
//

// AuthorizeTrakt ...
func AuthorizeTrakt(ctx *gin.Context) {
	err := trakt.Authorize(true)
	if err == nil {
		ctx.String(200, "")
	} else {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
		ctx.String(200, "")
	}
}

//
// Main lists
//

// WatchlistMovies ...
func WatchlistMovies(ctx *gin.Context) {
	movies, err := trakt.WatchlistMovies()
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	}
	renderTraktMovies(ctx, movies, -1, 0)
}

// WatchlistShows ...
func WatchlistShows(ctx *gin.Context) {
	shows, err := trakt.WatchlistShows()
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	}
	renderTraktShows(ctx, shows, -1, 0)
}

// CollectionMovies ...
func CollectionMovies(ctx *gin.Context) {
	movies, err := trakt.CollectionMovies()
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	}
	renderTraktMovies(ctx, movies, -1, 0)
}

// CollectionShows ...
func CollectionShows(ctx *gin.Context) {
	shows, err := trakt.CollectionShows()
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	}
	renderTraktShows(ctx, shows, -1, 0)
}

// UserlistMovies ...
func UserlistMovies(ctx *gin.Context) {
	listID := ctx.Params.ByName("listId")
	pageParam := ctx.DefaultQuery("page", "1")
	page, _ := strconv.Atoi(pageParam)
	movies, err := trakt.ListItemsMovies(listID, true)
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	}
	renderTraktMovies(ctx, movies, -1, page)
}

// UserlistShows ...
func UserlistShows(ctx *gin.Context) {
	listID := ctx.Params.ByName("listId")
	pageParam := ctx.DefaultQuery("page", "1")
	page, _ := strconv.Atoi(pageParam)
	shows, err := trakt.ListItemsShows(listID, true)
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	}
	renderTraktShows(ctx, shows, -1, page)
}

// func WatchlistSeasons(ctx *gin.Context) {
// 	renderTraktSeasons(trakt.Watchlist("seasons", pageParam), ctx, page)
// }

// func WatchlistEpisodes(ctx *gin.Context) {
// 	renderTraktEpisodes(trakt.Watchlist("episodes", pageParam), ctx, page)
// }

//
// Main lists actions
//

// AddMovieToWatchlist ...
func AddMovieToWatchlist(ctx *gin.Context) {
	tmdbID := ctx.Params.ByName("tmdbId")
	resp, err := trakt.AddToWatchlist("movies", tmdbID)
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	} else if resp.Status() != 201 {
		xbmc.Notify("Elementum", fmt.Sprintf("Failed with %d status code", resp.Status()), config.AddonIcon())
	} else {
		xbmc.Notify("Elementum", "Movie added to watchlist", config.AddonIcon())
		os.Remove(filepath.Join(config.Get().Info.Profile, "cache", "com.trakt.watchlist.movies"))
		os.Remove(filepath.Join(config.Get().Info.Profile, "cache", "com.trakt.movies.watchlist"))
		clearPageCache(ctx)
	}
}

// RemoveMovieFromWatchlist ...
func RemoveMovieFromWatchlist(ctx *gin.Context) {
	tmdbID := ctx.Params.ByName("tmdbId")
	resp, err := trakt.RemoveFromWatchlist("movies", tmdbID)
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	} else if resp.Status() != 200 {
		xbmc.Notify("Elementum", fmt.Sprintf("Failed with %d status code", resp.Status()), config.AddonIcon())
	} else {
		xbmc.Notify("Elementum", "Movie removed from watchlist", config.AddonIcon())
		os.Remove(filepath.Join(config.Get().Info.Profile, "cache", "com.trakt.watchlist.movies"))
		os.Remove(filepath.Join(config.Get().Info.Profile, "cache", "com.trakt.movies.watchlist"))
		clearPageCache(ctx)
	}
}

// AddShowToWatchlist ...
func AddShowToWatchlist(ctx *gin.Context) {
	tmdbID := ctx.Params.ByName("showId")
	resp, err := trakt.AddToWatchlist("shows", tmdbID)
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	} else if resp.Status() != 201 {
		xbmc.Notify("Elementum", fmt.Sprintf("Failed %d", resp.Status()), config.AddonIcon())
	} else {
		xbmc.Notify("Elementum", "Show added to watchlist", config.AddonIcon())
		os.Remove(filepath.Join(config.Get().Info.Profile, "cache", "com.trakt.watchlist.shows"))
		os.Remove(filepath.Join(config.Get().Info.Profile, "cache", "com.trakt.shows.watchlist"))
		clearPageCache(ctx)
	}
}

// RemoveShowFromWatchlist ...
func RemoveShowFromWatchlist(ctx *gin.Context) {
	tmdbID := ctx.Params.ByName("showId")
	resp, err := trakt.RemoveFromWatchlist("shows", tmdbID)
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	} else if resp.Status() != 200 {
		xbmc.Notify("Elementum", fmt.Sprintf("Failed with %d status code", resp.Status()), config.AddonIcon())
	} else {
		xbmc.Notify("Elementum", "Show removed from watchlist", config.AddonIcon())
		os.Remove(filepath.Join(config.Get().Info.Profile, "cache", "com.trakt.watchlist.shows"))
		os.Remove(filepath.Join(config.Get().Info.Profile, "cache", "com.trakt.shows.watchlist"))
		clearPageCache(ctx)
	}
}

// AddMovieToCollection ...
func AddMovieToCollection(ctx *gin.Context) {
	tmdbID := ctx.Params.ByName("tmdbId")
	resp, err := trakt.AddToCollection("movies", tmdbID)
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	} else if resp.Status() != 201 {
		xbmc.Notify("Elementum", fmt.Sprintf("Failed with %d status code", resp.Status()), config.AddonIcon())
	} else {
		xbmc.Notify("Elementum", "Movie added to collection", config.AddonIcon())
		os.Remove(filepath.Join(config.Get().Info.Profile, "cache", "com.trakt.collection.movies"))
		os.Remove(filepath.Join(config.Get().Info.Profile, "cache", "com.trakt.movies.collection"))
		clearPageCache(ctx)
	}
}

// RemoveMovieFromCollection ...
func RemoveMovieFromCollection(ctx *gin.Context) {
	tmdbID := ctx.Params.ByName("tmdbId")
	resp, err := trakt.RemoveFromCollection("movies", tmdbID)
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	} else if resp.Status() != 200 {
		xbmc.Notify("Elementum", fmt.Sprintf("Failed with %d status code", resp.Status()), config.AddonIcon())
	} else {
		xbmc.Notify("Elementum", "Movie removed from collection", config.AddonIcon())
		os.Remove(filepath.Join(config.Get().Info.Profile, "cache", "com.trakt.collection.movies"))
		os.Remove(filepath.Join(config.Get().Info.Profile, "cache", "com.trakt.movies.collection"))
		clearPageCache(ctx)
	}
}

// AddShowToCollection ...
func AddShowToCollection(ctx *gin.Context) {
	tmdbID := ctx.Params.ByName("showId")
	resp, err := trakt.AddToCollection("shows", tmdbID)
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	} else if resp.Status() != 201 {
		xbmc.Notify("Elementum", fmt.Sprintf("Failed with %d status code", resp.Status()), config.AddonIcon())
	} else {
		xbmc.Notify("Elementum", "Show added to collection", config.AddonIcon())
		os.Remove(filepath.Join(config.Get().Info.Profile, "cache", "com.trakt.collection.shows"))
		os.Remove(filepath.Join(config.Get().Info.Profile, "cache", "com.trakt.shows.collection"))
		clearPageCache(ctx)
	}
}

// RemoveShowFromCollection ...
func RemoveShowFromCollection(ctx *gin.Context) {
	tmdbID := ctx.Params.ByName("showId")
	resp, err := trakt.RemoveFromCollection("shows", tmdbID)
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	} else if resp.Status() != 200 {
		xbmc.Notify("Elementum", fmt.Sprintf("Failed with %d status code", resp.Status()), config.AddonIcon())
	} else {
		xbmc.Notify("Elementum", "Show removed from collection", config.AddonIcon())
		os.Remove(filepath.Join(config.Get().Info.Profile, "cache", "com.trakt.collection.shows"))
		os.Remove(filepath.Join(config.Get().Info.Profile, "cache", "com.trakt.shows.collection"))
		clearPageCache(ctx)
	}
}

// func AddEpisodeToWatchlist(ctx *gin.Context) {
// 	tmdbId := ctx.Params.ByName("episodeId")
// 	resp, err := trakt.AddToWatchlist("episodes", tmdbId)
// 	if err != nil {
// 		xbmc.Notify("Elementum", fmt.Sprintf("Failed: %s", err), config.AddonIcon())
// 	} else if resp.Status() != 201 {
// 		xbmc.Notify("Elementum", fmt.Sprintf("Failed: %d", resp.Status()), config.AddonIcon())
// 	} else {
// 		xbmc.Notify("Elementum", "Episode added to watchlist", config.AddonIcon())
// 	}
// }

func renderTraktMovies(ctx *gin.Context, movies []*trakt.Movies, total int, page int) {
	hasNextPage := 0
	if page > 0 {
		resultsPerPage := config.Get().ResultsPerPage

		if total == -1 {
			total = len(movies)
		}
		if total > resultsPerPage {
			if page*resultsPerPage < total {
				hasNextPage = 1
			}
		}

		if len(movies) > resultsPerPage {
			start := (page - 1) % trakt.PagesAtOnce * resultsPerPage
			movies = movies[start : start+resultsPerPage]
		}
	}

	items := make(xbmc.ListItems, 0, len(movies)+hasNextPage)

	for _, movieListing := range movies {
		if movieListing == nil {
			continue
		}
		movie := movieListing.Movie
		if movie == nil {
			continue
		}
		item := movie.ToListItem()

		playURL := URLForXBMC("/movie/%d/play", movie.IDs.TMDB)
		linksURL := URLForXBMC("/movie/%d/links", movie.IDs.TMDB)

		defaultURL := linksURL
		contextLabel := playLabel
		contextURL := playURL
		if config.Get().ChooseStreamAuto == true {
			defaultURL = playURL
			contextLabel = linksLabel
			contextURL = linksURL
		}

		item.Path = defaultURL

		tmdbID := strconv.Itoa(movie.IDs.TMDB)
		libraryAction := []string{"LOCALIZE[30252]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/library/movie/add/%d", movie.IDs.TMDB))}
		if _, err := isDuplicateMovie(tmdbID); err != nil || isAddedToLibrary(tmdbID, Movie) {
			libraryAction = []string{"LOCALIZE[30253]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/library/movie/remove/%d", movie.IDs.TMDB))}
		}

		watchlistAction := []string{"LOCALIZE[30255]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/movie/%d/watchlist/add", movie.IDs.TMDB))}
		if inMoviesWatchlist(movie.IDs.TMDB) {
			watchlistAction = []string{"LOCALIZE[30256]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/movie/%d/watchlist/remove", movie.IDs.TMDB))}
		}

		collectionAction := []string{"LOCALIZE[30258]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/movie/%d/collection/add", movie.IDs.TMDB))}
		if inMoviesCollection(movie.IDs.TMDB) {
			collectionAction = []string{"LOCALIZE[30259]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/movie/%d/collection/remove", movie.IDs.TMDB))}
		}

		if config.Get().Platform.Kodi < 17 {
			item.ContextMenu = [][]string{
				[]string{contextLabel, fmt.Sprintf("XBMC.PlayMedia(%s)", contextURL)},
				[]string{"LOCALIZE[30203]", "XBMC.Action(Info)"},
				[]string{"LOCALIZE[30268]", "XBMC.Action(ToggleWatched)"},
				[]string{"LOCALIZE[30034]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/setviewmode/movies"))},
				libraryAction,
				watchlistAction,
				collectionAction,
			}
		} else {
			item.ContextMenu = [][]string{
				[]string{contextLabel, fmt.Sprintf("XBMC.PlayMedia(%s)", contextURL)},
				libraryAction,
				watchlistAction,
				collectionAction,
				[]string{"LOCALIZE[30034]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/setviewmode/movies"))},
			}
		}
		item.IsPlayable = true
		items = append(items, item)
	}
	if page >= 0 && hasNextPage > 0 {
		path := ctx.Request.URL.Path
		nextpage := &xbmc.ListItem{
			Label:     "LOCALIZE[30218]",
			Path:      URLForXBMC(fmt.Sprintf("%s?page=%d", path, page+1)),
			Thumbnail: config.AddonResource("img", "nextpage.png"),
		}
		items = append(items, nextpage)
	}
	ctx.JSON(200, xbmc.NewView("movies", items))
}

// TraktPopularMovies ...
func TraktPopularMovies(ctx *gin.Context) {
	pageParam := ctx.DefaultQuery("page", "1")
	page, _ := strconv.Atoi(pageParam)
	movies, total, err := trakt.TopMovies("popular", pageParam)
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	}
	renderTraktMovies(ctx, movies, total, page)
}

// TraktTrendingMovies ...
func TraktTrendingMovies(ctx *gin.Context) {
	pageParam := ctx.DefaultQuery("page", "1")
	page, _ := strconv.Atoi(pageParam)
	movies, total, err := trakt.TopMovies("trending", pageParam)
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	}
	renderTraktMovies(ctx, movies, total, page)
}

// TraktMostPlayedMovies ...
func TraktMostPlayedMovies(ctx *gin.Context) {
	pageParam := ctx.DefaultQuery("page", "1")
	page, _ := strconv.Atoi(pageParam)
	movies, total, err := trakt.TopMovies("played", pageParam)
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	}
	renderTraktMovies(ctx, movies, total, page)
}

// TraktMostWatchedMovies ...
func TraktMostWatchedMovies(ctx *gin.Context) {
	pageParam := ctx.DefaultQuery("page", "1")
	page, _ := strconv.Atoi(pageParam)
	movies, total, err := trakt.TopMovies("watched", pageParam)
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	}
	renderTraktMovies(ctx, movies, total, page)
}

// TraktMostCollectedMovies ...
func TraktMostCollectedMovies(ctx *gin.Context) {
	pageParam := ctx.DefaultQuery("page", "1")
	page, _ := strconv.Atoi(pageParam)
	movies, total, err := trakt.TopMovies("collected", pageParam)
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	}
	renderTraktMovies(ctx, movies, total, page)
}

// TraktMostAnticipatedMovies ...
func TraktMostAnticipatedMovies(ctx *gin.Context) {
	pageParam := ctx.DefaultQuery("page", "1")
	page, _ := strconv.Atoi(pageParam)
	movies, total, err := trakt.TopMovies("anticipated", pageParam)
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	}
	renderTraktMovies(ctx, movies, total, page)
}

// TraktBoxOffice ...
func TraktBoxOffice(ctx *gin.Context) {
	movies, _, err := trakt.TopMovies("boxoffice", "1")
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	}
	renderTraktMovies(ctx, movies, -1, 0)
}

func renderTraktShows(ctx *gin.Context, shows []*trakt.Shows, total int, page int) {
	hasNextPage := 0
	if page > 0 {
		resultsPerPage := config.Get().ResultsPerPage

		if total == -1 {
			total = len(shows)
		}
		if total > resultsPerPage {
			if page*resultsPerPage < total {
				hasNextPage = 1
			}
		}

		if len(shows) >= resultsPerPage {
			start := (page - 1) % trakt.PagesAtOnce * resultsPerPage
			shows = shows[start : start+resultsPerPage]
		}
	}

	items := make(xbmc.ListItems, 0, len(shows)+hasNextPage)

	for _, showListing := range shows {
		if showListing == nil {
			continue
		}
		show := showListing.Show
		if show == nil {
			continue
		}
		item := show.ToListItem()
		item.Path = URLForXBMC("/show/%d/seasons", show.IDs.TMDB)

		tmdbID := strconv.Itoa(show.IDs.TMDB)
		libraryAction := []string{"LOCALIZE[30252]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/library/show/add/%d", show.IDs.TMDB))}
		if _, err := isDuplicateShow(tmdbID); err != nil || isAddedToLibrary(tmdbID, Show) {
			libraryAction = []string{"LOCALIZE[30253]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/library/show/remove/%d", show.IDs.TMDB))}
		}
		mergeAction := []string{"LOCALIZE[30283]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/library/show/add/%d?merge=true", show.IDs.TMDB))}

		watchlistAction := []string{"LOCALIZE[30255]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/show/%d/watchlist/add", show.IDs.TMDB))}
		if inShowsWatchlist(show.IDs.TMDB) {
			watchlistAction = []string{"LOCALIZE[30256]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/show/%d/watchlist/remove", show.IDs.TMDB))}
		}

		collectionAction := []string{"LOCALIZE[30258]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/show/%d/collection/add", show.IDs.TMDB))}
		if inShowsCollection(show.IDs.TMDB) {
			collectionAction = []string{"LOCALIZE[30259]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/show/%d/collection/remove", show.IDs.TMDB))}
		}

		item.ContextMenu = [][]string{
			libraryAction,
			mergeAction,
			watchlistAction,
			collectionAction,
			[]string{"LOCALIZE[30035]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/setviewmode/tvshows"))},
		}
		if config.Get().Platform.Kodi < 17 {
			item.ContextMenu = append(item.ContextMenu, []string{"LOCALIZE[30203]", "XBMC.Action(Info)"})
		}
		items = append(items, item)
	}
	if page >= 0 && hasNextPage > 0 {
		path := ctx.Request.URL.Path
		nextpage := &xbmc.ListItem{
			Label:     "LOCALIZE[30218]",
			Path:      URLForXBMC(fmt.Sprintf("%s?page=%d", path, page+1)),
			Thumbnail: config.AddonResource("img", "nextpage.png"),
		}
		items = append(items, nextpage)
	}
	ctx.JSON(200, xbmc.NewView("tvshows", items))
}

// TraktPopularShows ...
func TraktPopularShows(ctx *gin.Context) {
	pageParam := ctx.DefaultQuery("page", "1")
	page, _ := strconv.Atoi(pageParam)
	shows, total, err := trakt.TopShows("popular", pageParam)
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	}
	renderTraktShows(ctx, shows, total, page)
}

// TraktTrendingShows ...
func TraktTrendingShows(ctx *gin.Context) {
	pageParam := ctx.DefaultQuery("page", "1")
	page, _ := strconv.Atoi(pageParam)
	shows, total, err := trakt.TopShows("trending", pageParam)
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	}
	renderTraktShows(ctx, shows, total, page)
}

// TraktMostPlayedShows ...
func TraktMostPlayedShows(ctx *gin.Context) {
	pageParam := ctx.DefaultQuery("page", "1")
	page, _ := strconv.Atoi(pageParam)
	shows, total, err := trakt.TopShows("played", pageParam)
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	}
	renderTraktShows(ctx, shows, total, page)
}

// TraktMostWatchedShows ...
func TraktMostWatchedShows(ctx *gin.Context) {
	pageParam := ctx.DefaultQuery("page", "1")
	page, _ := strconv.Atoi(pageParam)
	shows, total, err := trakt.TopShows("watched", pageParam)
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	}
	renderTraktShows(ctx, shows, total, page)
}

// TraktMostCollectedShows ...
func TraktMostCollectedShows(ctx *gin.Context) {
	pageParam := ctx.DefaultQuery("page", "1")
	page, _ := strconv.Atoi(pageParam)
	shows, total, err := trakt.TopShows("collected", pageParam)
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	}
	renderTraktShows(ctx, shows, total, page)
}

// TraktMostAnticipatedShows ...
func TraktMostAnticipatedShows(ctx *gin.Context) {
	pageParam := ctx.DefaultQuery("page", "1")
	page, _ := strconv.Atoi(pageParam)
	shows, total, err := trakt.TopShows("anticipated", pageParam)
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	}
	renderTraktShows(ctx, shows, total, page)
}

//
// Calendars
//

// TraktMyShows ...
func TraktMyShows(ctx *gin.Context) {
	pageParam := ctx.DefaultQuery("page", "1")
	page, _ := strconv.Atoi(pageParam)
	shows, total, err := trakt.CalendarShows("my/shows", pageParam)
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	}
	renderCalendarShows(ctx, shows, total, page)
}

// TraktMyNewShows ...
func TraktMyNewShows(ctx *gin.Context) {
	pageParam := ctx.DefaultQuery("page", "1")
	page, _ := strconv.Atoi(pageParam)
	shows, total, err := trakt.CalendarShows("my/shows/new", pageParam)
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	}
	renderCalendarShows(ctx, shows, total, page)
}

// TraktMyPremieres ...
func TraktMyPremieres(ctx *gin.Context) {
	pageParam := ctx.DefaultQuery("page", "1")
	page, _ := strconv.Atoi(pageParam)
	shows, total, err := trakt.CalendarShows("my/shows/premieres", pageParam)
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	}
	renderCalendarShows(ctx, shows, total, page)
}

// TraktMyMovies ...
func TraktMyMovies(ctx *gin.Context) {
	pageParam := ctx.DefaultQuery("page", "1")
	page, _ := strconv.Atoi(pageParam)
	movies, total, err := trakt.CalendarMovies("my/movies", pageParam)
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	}
	renderCalendarMovies(ctx, movies, total, page)
}

// TraktMyReleases ...
func TraktMyReleases(ctx *gin.Context) {
	pageParam := ctx.DefaultQuery("page", "1")
	page, _ := strconv.Atoi(pageParam)
	movies, total, err := trakt.CalendarMovies("my/dvd", pageParam)
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	}
	renderCalendarMovies(ctx, movies, total, page)
}

// TraktAllShows ...
func TraktAllShows(ctx *gin.Context) {
	pageParam := ctx.DefaultQuery("page", "1")
	page, _ := strconv.Atoi(pageParam)
	shows, total, err := trakt.CalendarShows("all/shows", pageParam)
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	}
	renderCalendarShows(ctx, shows, total, page)
}

// TraktAllNewShows ...
func TraktAllNewShows(ctx *gin.Context) {
	pageParam := ctx.DefaultQuery("page", "1")
	page, _ := strconv.Atoi(pageParam)
	shows, total, err := trakt.CalendarShows("all/shows/new", pageParam)
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	}
	renderCalendarShows(ctx, shows, total, page)
}

// TraktAllPremieres ...
func TraktAllPremieres(ctx *gin.Context) {
	pageParam := ctx.DefaultQuery("page", "1")
	page, _ := strconv.Atoi(pageParam)
	shows, total, err := trakt.CalendarShows("all/shows/premieres", pageParam)
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	}
	renderCalendarShows(ctx, shows, total, page)
}

// TraktAllMovies ...
func TraktAllMovies(ctx *gin.Context) {
	pageParam := ctx.DefaultQuery("page", "1")
	page, _ := strconv.Atoi(pageParam)
	movies, total, err := trakt.CalendarMovies("all/movies", pageParam)
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	}
	renderCalendarMovies(ctx, movies, total, page)
}

// TraktAllReleases ...
func TraktAllReleases(ctx *gin.Context) {
	pageParam := ctx.DefaultQuery("page", "1")
	page, _ := strconv.Atoi(pageParam)
	movies, total, err := trakt.CalendarMovies("all/dvd", pageParam)
	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
	}
	renderCalendarMovies(ctx, movies, total, page)
}

func renderCalendarMovies(ctx *gin.Context, movies []*trakt.CalendarMovie, total int, page int) {
	hasNextPage := 0
	if page > 0 {
		resultsPerPage := config.Get().ResultsPerPage

		if total == -1 {
			total = len(movies)
		}
		if total > resultsPerPage {
			if page*resultsPerPage < total {
				hasNextPage = 1
			}
		}

		if len(movies) > resultsPerPage {
			start := (page - 1) % trakt.PagesAtOnce * resultsPerPage
			movies = movies[start : start+resultsPerPage]
		}
	}

	items := make(xbmc.ListItems, 0, len(movies)+hasNextPage)

	for _, movieListing := range movies {
		if movieListing == nil {
			continue
		}
		movie := movieListing.Movie
		if movie == nil {
			continue
		}
		item := movie.ToListItem()
		label := fmt.Sprintf("%s - %s", movieListing.Released, movie.Title)
		item.Label = label
		item.Info.Title = label

		playURL := URLForXBMC("/movie/%d/play", movie.IDs.TMDB)
		linksURL := URLForXBMC("/movie/%d/links", movie.IDs.TMDB)

		defaultURL := linksURL
		contextLabel := playLabel
		contextURL := playURL
		if config.Get().ChooseStreamAuto == true {
			defaultURL = playURL
			contextLabel = linksLabel
			contextURL = linksURL
		}

		item.Path = defaultURL

		tmdbID := strconv.Itoa(movie.IDs.TMDB)
		libraryAction := []string{"LOCALIZE[30252]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/library/movie/add/%d", movie.IDs.TMDB))}
		if _, err := isDuplicateMovie(tmdbID); err != nil || isAddedToLibrary(tmdbID, Movie) {
			libraryAction = []string{"LOCALIZE[30253]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/library/movie/remove/%d", movie.IDs.TMDB))}
		}

		watchlistAction := []string{"LOCALIZE[30255]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/movie/%d/watchlist/add", movie.IDs.TMDB))}
		if inMoviesWatchlist(movie.IDs.TMDB) {
			watchlistAction = []string{"LOCALIZE[30256]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/movie/%d/watchlist/remove", movie.IDs.TMDB))}
		}

		collectionAction := []string{"LOCALIZE[30258]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/movie/%d/collection/add", movie.IDs.TMDB))}
		if inMoviesCollection(movie.IDs.TMDB) {
			collectionAction = []string{"LOCALIZE[30259]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/movie/%d/collection/remove", movie.IDs.TMDB))}
		}

		if config.Get().Platform.Kodi < 17 {
			item.ContextMenu = [][]string{
				[]string{contextLabel, fmt.Sprintf("XBMC.PlayMedia(%s)", contextURL)},
				[]string{"LOCALIZE[30203]", "XBMC.Action(Info)"},
				[]string{"LOCALIZE[30268]", "XBMC.Action(ToggleWatched)"},
				[]string{"LOCALIZE[30034]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/setviewmode/movies"))},
				libraryAction,
				watchlistAction,
				collectionAction,
			}
		} else {
			item.ContextMenu = [][]string{
				[]string{contextLabel, fmt.Sprintf("XBMC.PlayMedia(%s)", contextURL)},
				libraryAction,
				watchlistAction,
				collectionAction,
				[]string{"LOCALIZE[30034]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/setviewmode/movies"))},
			}
		}
		item.IsPlayable = true
		items = append(items, item)
	}
	if page >= 0 && hasNextPage > 0 {
		path := ctx.Request.URL.Path
		nextpage := &xbmc.ListItem{
			Label:     "LOCALIZE[30218]",
			Path:      URLForXBMC(fmt.Sprintf("%s?page=%d", path, page+1)),
			Thumbnail: config.AddonResource("img", "nextpage.png"),
		}
		items = append(items, nextpage)
	}
	ctx.JSON(200, xbmc.NewView("movies", items))
}

func renderCalendarShows(ctx *gin.Context, shows []*trakt.CalendarShow, total int, page int) {
	hasNextPage := 0
	if page > 0 {
		resultsPerPage := config.Get().ResultsPerPage

		if total == -1 {
			total = len(shows)
		}
		if total > resultsPerPage {
			if page*resultsPerPage < total {
				hasNextPage = 1
			}
		}

		if len(shows) >= resultsPerPage {
			start := (page - 1) % trakt.PagesAtOnce * resultsPerPage
			shows = shows[start : start+resultsPerPage]
		}
	}

	items := make(xbmc.ListItems, 0, len(shows)+hasNextPage)

	for _, showListing := range shows {
		if showListing == nil {
			continue
		}
		show := showListing.Show
		if show == nil {
			continue
		}
		item := show.ToListItem()
		episode := showListing.Episode
		label := fmt.Sprintf("%s - %s | %dx%02d %s", []byte(showListing.FirstAired)[:10], item.Label, episode.Season, episode.Number, episode.Title)
		item.Label = label
		item.Info.Title = label

		itemPath := URLQuery(URLForXBMC("/search"), "q", fmt.Sprintf("%s S%02dE%02d", show.Title, episode.Season, episode.Number))
		if episode.Season > 100 {
			itemPath = URLQuery(URLForXBMC("/search"), "q", fmt.Sprintf("%s %d %d", show.Title, episode.Number, episode.Season))
		}
		item.Path = itemPath

		tmdbID := strconv.Itoa(show.IDs.TMDB)
		libraryAction := []string{"LOCALIZE[30252]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/library/show/add/%d", show.IDs.TMDB))}
		if _, err := isDuplicateShow(tmdbID); err != nil || isAddedToLibrary(tmdbID, Show) {
			libraryAction = []string{"LOCALIZE[30253]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/library/show/remove/%d", show.IDs.TMDB))}
		}
		mergeAction := []string{"LOCALIZE[30283]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/library/show/add/%d?merge=true", show.IDs.TMDB))}

		watchlistAction := []string{"LOCALIZE[30255]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/show/%d/watchlist/add", show.IDs.TMDB))}
		if inShowsWatchlist(show.IDs.TMDB) {
			watchlistAction = []string{"LOCALIZE[30256]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/show/%d/watchlist/remove", show.IDs.TMDB))}
		}

		collectionAction := []string{"LOCALIZE[30258]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/show/%d/collection/add", show.IDs.TMDB))}
		if inShowsCollection(show.IDs.TMDB) {
			collectionAction = []string{"LOCALIZE[30259]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/show/%d/collection/remove", show.IDs.TMDB))}
		}

		item.ContextMenu = [][]string{
			libraryAction,
			mergeAction,
			watchlistAction,
			collectionAction,
			[]string{"LOCALIZE[30203]", "XBMC.Action(Info)"},
			[]string{"LOCALIZE[30268]", "XBMC.Action(ToggleWatched)"},
			[]string{"LOCALIZE[30035]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/setviewmode/tvshows"))},
		}
		item.IsPlayable = true

		items = append(items, item)
	}
	if page >= 0 && hasNextPage > 0 {
		path := ctx.Request.URL.Path
		nextpage := &xbmc.ListItem{
			Label:     "LOCALIZE[30218]",
			Path:      URLForXBMC(fmt.Sprintf("%s?page=%d", path, page+1)),
			Thumbnail: config.AddonResource("img", "nextpage.png"),
		}
		items = append(items, nextpage)
	}
	ctx.JSON(200, xbmc.NewView("tvshows", items))
}
