package api

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/elgatito/elementum/bittorrent"
	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/library"
	"github.com/elgatito/elementum/providers"
	"github.com/elgatito/elementum/tmdb"
	"github.com/elgatito/elementum/trakt"
	"github.com/elgatito/elementum/xbmc"
	"github.com/gin-gonic/gin"
)

// Maps TMDB movie genre ids to slugs for images
var genreSlugs = map[int]string{
	28:    "action",
	10759: "action",
	12:    "adventure",
	16:    "animation",
	35:    "comedy",
	80:    "crime",
	99:    "documentary",
	18:    "drama",
	10761: "education",
	10751: "family",
	14:    "fantasy",
	10769: "foreign",
	36:    "history",
	27:    "horror",
	10762: "kids",
	10402: "music",
	9648:  "mystery",
	10763: "news",
	10764: "reality",
	10749: "romance",
	878:   "scifi",
	10765: "scifi",
	10766: "soap",
	10767: "talk",
	10770: "tv",
	53:    "thriller",
	10752: "war",
	10768: "war",
	37:    "western",
}

// MoviesIndex ...
func MoviesIndex(ctx *gin.Context) {
	items := xbmc.ListItems{
		{Label: "LOCALIZE[30056]", Path: URLForXBMC("/movies/trakt/"), Thumbnail: config.AddonResource("img", "trakt.png")},
		{Label: "LOCALIZE[30209]", Path: URLForXBMC("/movies/search"), Thumbnail: config.AddonResource("img", "search.png")},
		{Label: "LOCALIZE[30246]", Path: URLForXBMC("/movies/trakt/trending"), Thumbnail: config.AddonResource("img", "trending.png")},
		{Label: "LOCALIZE[30210]", Path: URLForXBMC("/movies/popular"), Thumbnail: config.AddonResource("img", "popular.png")},
		{Label: "LOCALIZE[30211]", Path: URLForXBMC("/movies/top"), Thumbnail: config.AddonResource("img", "top_rated.png")},
		{Label: "LOCALIZE[30212]", Path: URLForXBMC("/movies/mostvoted"), Thumbnail: config.AddonResource("img", "most_voted.png")},
		{Label: "LOCALIZE[30236]", Path: URLForXBMC("/movies/recent"), Thumbnail: config.AddonResource("img", "clock.png")},
		{Label: "LOCALIZE[30213]", Path: URLForXBMC("/movies/imdb250"), Thumbnail: config.AddonResource("img", "imdb.png")},
		{Label: "LOCALIZE[30289]", Path: URLForXBMC("/movies/genres"), Thumbnail: config.AddonResource("img", "genre_comedy.png")},
	}
	itemsWithContext := make(xbmc.ListItems, 0)
	for _, item := range items {
		item.ContextMenu = [][]string{
			[]string{"LOCALIZE[30142]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/setviewmode/menus_movies"))},
		}
		itemsWithContext = append(itemsWithContext, item)
	}
	ctx.JSON(200, xbmc.NewView("menus_movies", itemsWithContext))
}

// MovieGenres ...
func MovieGenres(ctx *gin.Context) {
	items := make(xbmc.ListItems, 0)
	for _, genre := range tmdb.GetMovieGenres(config.Get().Language) {
		slug, _ := genreSlugs[genre.ID]
		items = append(items, &xbmc.ListItem{
			Label:     genre.Name,
			Path:      URLForXBMC("/movies/popular/%s", strconv.Itoa(genre.ID)),
			Thumbnail: config.AddonResource("img", fmt.Sprintf("genre_%s.png", slug)),
			ContextMenu: [][]string{
				[]string{"LOCALIZE[30236]", fmt.Sprintf("Container.Update(%s)", URLForXBMC("/movies/recent/%s", strconv.Itoa(genre.ID)))},
				[]string{"LOCALIZE[30144]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/setviewmode/menus_movies_genres"))},
			},
		})
	}
	ctx.JSON(200, xbmc.NewView("menus_movies_genres", items))
}

// MoviesTrakt ...
func MoviesTrakt(ctx *gin.Context) {
	items := xbmc.ListItems{
		{Label: "LOCALIZE[30263]", Path: URLForXBMC("/movies/trakt/lists/"), Thumbnail: config.AddonResource("img", "trakt.png")},
		{
			Label:     "LOCALIZE[30254]",
			Path:      URLForXBMC("/movies/trakt/watchlist"),
			Thumbnail: config.AddonResource("img", "trakt.png"),
			ContextMenu: [][]string{
				[]string{"LOCALIZE[30252]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/library/movie/list/add/watchlist"))},
			},
		},
		{
			Label:     "LOCALIZE[30257]",
			Path:      URLForXBMC("/movies/trakt/collection"),
			Thumbnail: config.AddonResource("img", "trakt.png"),
			ContextMenu: [][]string{
				[]string{"LOCALIZE[30252]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/library/movie/list/add/collection"))},
			},
		},
		{Label: "LOCALIZE[30290]", Path: URLForXBMC("/movies/trakt/calendars/"), Thumbnail: config.AddonResource("img", "most_anticipated.png")},
		{Label: "LOCALIZE[30246]", Path: URLForXBMC("/movies/trakt/trending"), Thumbnail: config.AddonResource("img", "trending.png")},
		{Label: "LOCALIZE[30210]", Path: URLForXBMC("/movies/trakt/popular"), Thumbnail: config.AddonResource("img", "popular.png")},
		{Label: "LOCALIZE[30247]", Path: URLForXBMC("/movies/trakt/played"), Thumbnail: config.AddonResource("img", "most_played.png")},
		{Label: "LOCALIZE[30248]", Path: URLForXBMC("/movies/trakt/watched"), Thumbnail: config.AddonResource("img", "most_watched.png")},
		{Label: "LOCALIZE[30249]", Path: URLForXBMC("/movies/trakt/collected"), Thumbnail: config.AddonResource("img", "most_collected.png")},
		{Label: "LOCALIZE[30250]", Path: URLForXBMC("/movies/trakt/anticipated"), Thumbnail: config.AddonResource("img", "most_anticipated.png")},
		{Label: "LOCALIZE[30251]", Path: URLForXBMC("/movies/trakt/boxoffice"), Thumbnail: config.AddonResource("img", "box_office.png")},
	}
	ctx.JSON(200, xbmc.NewView("menus_movies", items))
}

// MoviesTraktLists ...
func MoviesTraktLists(ctx *gin.Context) {
	items := xbmc.ListItems{}
	for _, list := range trakt.Userlists() {
		item := &xbmc.ListItem{
			Label:     list.Name,
			Path:      URLForXBMC("/movies/trakt/lists/id/%d", list.IDs.Trakt),
			Thumbnail: config.AddonResource("img", "trakt.png"),
			ContextMenu: [][]string{
				[]string{"LOCALIZE[30252]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/library/movie/list/add/%d", list.IDs.Trakt))},
			},
		}
		items = append(items, item)
	}
	ctx.JSON(200, xbmc.NewView("menus_movies", items))
}

// CalendarMovies ...
func CalendarMovies(ctx *gin.Context) {
	items := xbmc.ListItems{
		{Label: "LOCALIZE[30291]", Path: URLForXBMC("/movies/trakt/calendars/movies"), Thumbnail: config.AddonResource("img", "box_office.png")},
		{Label: "LOCALIZE[30292]", Path: URLForXBMC("/movies/trakt/calendars/releases"), Thumbnail: config.AddonResource("img", "tv.png")},
		{Label: "LOCALIZE[30293]", Path: URLForXBMC("/movies/trakt/calendars/allmovies"), Thumbnail: config.AddonResource("img", "box_office.png")},
		{Label: "LOCALIZE[30294]", Path: URLForXBMC("/movies/trakt/calendars/allreleases"), Thumbnail: config.AddonResource("img", "tv.png")},
	}
	ctx.JSON(200, xbmc.NewView("menus_movies", items))
}

func renderMovies(ctx *gin.Context, movies tmdb.Movies, page int, total int, query string) {
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
			start := (page - 1) % tmdb.PagesAtOnce * resultsPerPage
			end := start + resultsPerPage
			if end > len(movies) {
				end = len(movies)
			}
			movies = movies[start:end]
		}
	}

	items := make(xbmc.ListItems, 0, len(movies)+hasNextPage)

	for _, movie := range movies {
		if movie == nil {
			continue
		}
		item := movie.ToListItem()

		playLabel := "LOCALIZE[30023]"
		playURL := URLForXBMC("/movie/%d/play", movie.ID)
		linksLabel := "LOCALIZE[30202]"
		linksURL := URLForXBMC("/movie/%d/links", movie.ID)

		defaultURL := linksURL
		contextLabel := playLabel
		contextURL := playURL
		if config.Get().ChooseStreamAuto == true {
			defaultURL = playURL
			contextLabel = linksLabel
			contextURL = linksURL
		}

		item.Path = defaultURL

		tmdbID := strconv.Itoa(movie.ID)
		libraryAction := []string{"LOCALIZE[30252]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/library/movie/add/%d", movie.ID))}
		if _, err := library.IsDuplicateMovie(tmdbID); err != nil || library.IsAddedToLibrary(tmdbID, library.MovieType) {
			libraryAction = []string{"LOCALIZE[30253]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/library/movie/remove/%d", movie.ID))}
		}

		watchlistAction := []string{"LOCALIZE[30255]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/movie/%d/watchlist/add", movie.ID))}
		if inMoviesWatchlist(movie.ID) {
			watchlistAction = []string{"LOCALIZE[30256]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/movie/%d/watchlist/remove", movie.ID))}
		}

		collectionAction := []string{"LOCALIZE[30258]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/movie/%d/collection/add", movie.ID))}
		if inMoviesCollection(movie.ID) {
			collectionAction = []string{"LOCALIZE[30259]", fmt.Sprintf("XBMC.RunPlugin(%s)", URLForXBMC("/movie/%d/collection/remove", movie.ID))}
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
		item.Info.Trailer = URLForHTTP("/youtube/%s", item.Info.Trailer)
		item.IsPlayable = true
		items = append(items, item)
	}
	if page >= 0 && hasNextPage > 0 {
		path := ctx.Request.URL.Path
		nextPath := URLForXBMC(fmt.Sprintf("%s?page=%d", path, page+1))
		if query != "" {
			nextPath = URLForXBMC(fmt.Sprintf("%s?q=%s&page=%d", path, query, page+1))
		}
		next := &xbmc.ListItem{
			Label:     "LOCALIZE[30218]",
			Path:      nextPath,
			Thumbnail: config.AddonResource("img", "nextpage.png"),
		}
		items = append(items, next)
	}
	ctx.JSON(200, xbmc.NewView("movies", items))
}

// PopularMovies ...
func PopularMovies(ctx *gin.Context) {
	genre := ctx.Params.ByName("genre")
	if genre == "0" {
		genre = ""
	}
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	movies, total := tmdb.PopularMovies(genre, config.Get().Language, page)
	renderMovies(ctx, movies, page, total, "")
}

// RecentMovies ...
func RecentMovies(ctx *gin.Context) {
	genre := ctx.Params.ByName("genre")
	if genre == "0" {
		genre = ""
	}
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	movies, total := tmdb.RecentMovies(genre, config.Get().Language, page)
	renderMovies(ctx, movies, page, total, "")
}

// TopRatedMovies ...
func TopRatedMovies(ctx *gin.Context) {
	genre := ctx.Params.ByName("genre")
	if genre == "0" {
		genre = ""
	}
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	movies, total := tmdb.TopRatedMovies(genre, config.Get().Language, page)
	renderMovies(ctx, movies, page, total, "")
}

// IMDBTop250 ...
func IMDBTop250(ctx *gin.Context) {
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	movies, total := tmdb.GetIMDBList("522effe419c2955e9922fcf3", config.Get().Language, page)
	renderMovies(ctx, movies, page, total, "")
}

// MoviesMostVoted ...
func MoviesMostVoted(ctx *gin.Context) {
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	movies, total := tmdb.MostVotedMovies("", config.Get().Language, page)
	renderMovies(ctx, movies, page, total, "")
}

// SearchMovies ...
func SearchMovies(ctx *gin.Context) {
	ctx.Writer.Header().Set("Access-Control-Allow-Origin", "*")
	query := ctx.Query("q")
	keyboard := ctx.Query("keyboard")

	if len(query) == 0 {
		historyType := "movies"
		if len(keyboard) > 0 || searchHistoryEmpty(historyType) {
			query = xbmc.Keyboard("", "LOCALIZE[30206]")
			if len(query) == 0 {
				return
			}
			searchHistoryAppend(ctx, historyType, query)
		} else if !searchHistoryEmpty(historyType) {
			searchHistoryList(ctx, historyType)
		}
		return
	}

	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	movies, total := tmdb.SearchMovies(query, config.Get().Language, page)
	renderMovies(ctx, movies, page, total, query)
}

func movieLinks(tmdbID string) []*bittorrent.TorrentFile {
	log.Info("Searching links for:", tmdbID)

	movie := tmdb.GetMovieByID(tmdbID, config.Get().Language)

	log.Infof("Resolved %s to %s", tmdbID, movie.Title)

	searchers := providers.GetMovieSearchers()
	if len(searchers) == 0 {
		xbmc.Notify("Elementum", "LOCALIZE[30204]", config.AddonIcon())
	}

	return providers.SearchMovie(searchers, movie)
}

// MoviePlaySelector ...
func MoviePlaySelector(link string, btService *bittorrent.BTService, fromLibrary bool) gin.HandlerFunc {
	play := link == "play"

	if config.Get().ForceLinkType {
		if config.Get().ChooseStreamAuto {
			play = true
		} else {
			play = false
		}
	}

	if play {
		return MoviePlay(btService, fromLibrary)
	}
	return MovieLinks(btService, fromLibrary)
}

// MovieLinks ...
func MovieLinks(btService *bittorrent.BTService, fromLibrary bool) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		ctx.Writer.Header().Set("Access-Control-Allow-Origin", "*")

		tmdbID := ctx.Params.ByName("tmdbId")
		external := ctx.Query("external")
		library := ""
		if fromLibrary {
			library = "1"
		}

		movie := tmdb.GetMovieByID(tmdbID, config.Get().Language)
		if movie == nil {
			return
		}

		existingTorrent := ExistingTorrent(btService, movie.Title)
		if existingTorrent != "" && xbmc.DialogConfirm("Elementum", "LOCALIZE[30270]") {
			rURL := URLQuery(
				URLForXBMC("/play"), "uri", existingTorrent,
				"tmdb", tmdbID,
				"library", library,
				"type", "movie")
			if external != "" {
				xbmc.PlayURL(rURL)
			} else {
				ctx.Redirect(302, rURL)
			}
			return
		}

		if torrent := InTorrentsMap(tmdbID); torrent != nil {
			rURL := URLQuery(
				URLForXBMC("/play"), "uri", torrent.URI,
				"tmdb", tmdbID,
				"library", library,
				"type", "movie")
			if external != "" {
				xbmc.PlayURL(rURL)
			} else {
				ctx.Redirect(302, rURL)
			}
			return
		}

		torrents := movieLinks(tmdbID)

		if len(torrents) == 0 {
			xbmc.Notify("Elementum", "LOCALIZE[30205]", config.AddonIcon())
			return
		}

		choices := make([]string, 0, len(torrents))
		for _, torrent := range torrents {
			resolution := ""
			if torrent.Resolution > 0 {
				resolution = fmt.Sprintf("[B][COLOR %s]%s[/COLOR][/B] ", bittorrent.Colors[torrent.Resolution], bittorrent.Resolutions[torrent.Resolution])
			}

			info := make([]string, 0)
			if torrent.Size != "" {
				info = append(info, fmt.Sprintf("[B][%s][/B]", torrent.Size))
			}
			if torrent.RipType > 0 {
				info = append(info, bittorrent.Rips[torrent.RipType])
			}
			if torrent.VideoCodec > 0 {
				info = append(info, bittorrent.Codecs[torrent.VideoCodec])
			}
			if torrent.AudioCodec > 0 {
				info = append(info, bittorrent.Codecs[torrent.AudioCodec])
			}
			if torrent.Provider != "" {
				info = append(info, fmt.Sprintf(" - [B]%s[/B]", torrent.Provider))
			}

			multi := ""
			if torrent.Multi {
				multi = multiType
			}

			label := fmt.Sprintf("%s(%d / %d) %s\n%s\n%s%s",
				resolution,
				torrent.Seeds,
				torrent.Peers,
				strings.Join(info, " "),
				torrent.Name,
				torrent.Icon,
				multi,
			)
			choices = append(choices, label)
		}

		choice := xbmc.ListDialogLarge("LOCALIZE[30228]", movie.Title, choices...)
		if choice >= 0 {
			AddToTorrentsMap(tmdbID, torrents[choice])

			rURL := URLQuery(
				URLForXBMC("/play"), "uri", torrents[choice].URI,
				"tmdb", tmdbID,
				"library", library,
				"type", "movie")
			if external != "" {
				xbmc.PlayURL(rURL)
			} else {
				ctx.Redirect(302, rURL)
			}
		}
	}
}

// MoviePlay ...
func MoviePlay(btService *bittorrent.BTService, fromLibrary bool) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		ctx.Writer.Header().Set("Access-Control-Allow-Origin", "*")

		tmdbID := ctx.Params.ByName("tmdbId")
		external := ctx.Query("external")
		library := ""
		if fromLibrary {
			library = "1"
		}

		movie := tmdb.GetMovieByID(tmdbID, "")
		if movie == nil {
			return
		}

		existingTorrent := ExistingTorrent(btService, movie.Title)
		if existingTorrent != "" && xbmc.DialogConfirm("Elementum", "LOCALIZE[30270]") {
			rURL := URLQuery(
				URLForXBMC("/play"), "uri", existingTorrent,
				"tmdb", tmdbID,
				"library", library,
				"type", "movie")
			if external != "" {
				xbmc.PlayURL(rURL)
			} else {
				ctx.Redirect(302, rURL)
			}
			return
		}

		if torrent := InTorrentsMap(tmdbID); torrent != nil {
			rURL := URLQuery(
				URLForXBMC("/play"), "uri", torrent.URI,
				"tmdb", tmdbID,
				"library", library,
				"type", "movie")
			if external != "" {
				xbmc.PlayURL(rURL)
			} else {
				ctx.Redirect(302, rURL)
			}
			return
		}

		torrents := movieLinks(tmdbID)
		if len(torrents) == 0 {
			xbmc.Notify("Elementum", "LOCALIZE[30205]", config.AddonIcon())
			return
		}

		sort.Sort(sort.Reverse(providers.ByQuality(torrents)))

		AddToTorrentsMap(tmdbID, torrents[0])

		rURL := URLQuery(
			URLForXBMC("/play"), "uri", torrents[0].URI,
			"tmdb", tmdbID,
			"library", library,
			"type", "movie")
		if external != "" {
			xbmc.PlayURL(rURL)
		} else {
			ctx.Redirect(302, rURL)
		}
	}
}
