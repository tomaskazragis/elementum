package trakt

import (
	"fmt"
	"path"
	"time"
  "errors"
	"strconv"
	"strings"
	"math/rand"

	"github.com/jmcvetta/napping"
	"github.com/scakemyer/quasar/config"
	"github.com/scakemyer/quasar/cache"
	"github.com/scakemyer/quasar/tmdb"
	"github.com/scakemyer/quasar/xbmc"
)

// Fill fanart from TMDB
func setShowFanart(show *Show) *Show {
	if show.IDs == nil || show.IDs.TMDB == 0 {
		return show
	}
	tmdbImages := tmdb.GetShowImages(show.IDs.TMDB)
	if tmdbImages == nil {
		return show
	}
	if show.Images == nil {
		show.Images = &Images{}
		show.Images.Poster = &Sizes{}
		show.Images.Thumbnail = &Sizes{}
		show.Images.FanArt = &Sizes{}
		show.Images.Banner = &Sizes{}
	}
	if len(tmdbImages.Posters) > 0 {
		posterImage := tmdb.ImageURL(tmdbImages.Posters[0].FilePath, "w500")
		for _, image := range tmdbImages.Posters {
			if image.ISO_639_1 == config.Get().Language {
				posterImage = tmdb.ImageURL(image.FilePath, "w500")
			}
		}
		show.Images.Poster.Full = posterImage
		show.Images.Thumbnail.Full = posterImage
	}
	if len(tmdbImages.Backdrops) > 0 {
		backdropImage := tmdb.ImageURL(tmdbImages.Backdrops[0].FilePath, "w1280")
		for _, image := range tmdbImages.Backdrops {
			if image.ISO_639_1 == config.Get().Language {
				backdropImage = tmdb.ImageURL(image.FilePath, "w1280")
			}
		}
		show.Images.FanArt.Full = backdropImage
		show.Images.Banner.Full = backdropImage
	}
	return show
}

func setShowsFanart(shows []*Shows) []*Shows {
	for i, show := range shows {
		shows[i].Show = setShowFanart(show.Show)
	}
	return shows
}

func GetShow(Id string) (show *Show) {
	endPoint := fmt.Sprintf("shows/%s", Id)

	params := napping.Params{
		"extended": "full,images",
	}.AsUrlValues()

	cacheStore := cache.NewFileStore(path.Join(config.Get().ProfilePath, "cache"))
	key := fmt.Sprintf("com.trakt.show.%s", Id)
	if err := cacheStore.Get(key, &show); err != nil {
		resp, err := Get(endPoint, params)
		if err != nil {
			log.Error(err)
			xbmc.Notify("Quasar", fmt.Sprintf("Failed getting Trakt show (%s), check your logs.", Id), config.AddonIcon())
			return
		}
		if err := resp.Unmarshal(&show); err != nil {
			log.Warning(err)
		}
		show = setShowFanart(show)
		cacheStore.Set(key, show, cacheExpiration)
	}

	return
}

func GetShowByTMDB(tmdbId string) (show *Show) {
	endPoint := fmt.Sprintf("search/tmdb/%s?type=show", tmdbId)

	params := napping.Params{}.AsUrlValues()

	cacheStore := cache.NewFileStore(path.Join(config.Get().ProfilePath, "cache"))
	key := fmt.Sprintf("com.trakt.show.tmdb.%s", tmdbId)
	if err := cacheStore.Get(key, &show); err != nil {
		resp, err := Get(endPoint, params)
		if err != nil {
			log.Error(err)
			xbmc.Notify("Quasar", "Failed getting Trakt show using TMDB ID, check your logs.", config.AddonIcon())
			return
		}
		if err := resp.Unmarshal(&show); err != nil {
			log.Warning(err)
		}
		cacheStore.Set(key, show, cacheExpiration)
	}
	return
}

func GetShowByTVDB(tvdbId string) (show *Show) {
	endPoint := fmt.Sprintf("search/tvdb/%s?type=show", tvdbId)

	params := napping.Params{}.AsUrlValues()

	cacheStore := cache.NewFileStore(path.Join(config.Get().ProfilePath, "cache"))
	key := fmt.Sprintf("com.trakt.show.tvdb.%s", tvdbId)
	if err := cacheStore.Get(key, &show); err != nil {
		resp, err := Get(endPoint, params)
		if err != nil {
			log.Error(err)
			xbmc.Notify("Quasar", "Failed getting Trakt show using TVDB ID, check your logs.", config.AddonIcon())
			return
		}
		if err := resp.Unmarshal(&show); err != nil {
			log.Warning(err)
		}
		cacheStore.Set(key, show, cacheExpiration)
	}
	return
}

func GetEpisode(id string) (episode *Episode) {
	endPoint := fmt.Sprintf("search/trakt/%s?type=episode", id)

	params := napping.Params{}.AsUrlValues()

	cacheStore := cache.NewFileStore(path.Join(config.Get().ProfilePath, "cache"))
	key := fmt.Sprintf("com.trakt.episode.%s", id)
	if err := cacheStore.Get(key, &episode); err != nil {
		resp, err := Get(endPoint, params)
		if err != nil {
			log.Error(err)
			xbmc.Notify("Quasar", "Failed getting Trakt episode, check your logs.", config.AddonIcon())
			return
		}
		if err := resp.Unmarshal(&episode); err != nil {
			log.Warning(err)
		}
		cacheStore.Set(key, episode, cacheExpiration)
	}
	return
}

func GetEpisodeByTMDB(tmdbId string) (episode *Episode) {
	endPoint := fmt.Sprintf("search/tmdb/%s?type=episode", tmdbId)

	params := napping.Params{}.AsUrlValues()

	cacheStore := cache.NewFileStore(path.Join(config.Get().ProfilePath, "cache"))
	key := fmt.Sprintf("com.trakt.episode.tmdb.%s", tmdbId)
	if err := cacheStore.Get(key, &episode); err != nil {
		resp, err := Get(endPoint, params)
		if err != nil {
			log.Error(err)
			xbmc.Notify("Quasar", "Failed getting Trakt episode using TMDB ID, check your logs.", config.AddonIcon())
			return
		}
		if err := resp.Unmarshal(&episode); err != nil {
			log.Warning(err)
		}
		cacheStore.Set(key, episode, cacheExpiration)
	}
	return
}

func GetEpisodeByTVDB(tvdbId string) (episode *Episode) {
	endPoint := fmt.Sprintf("search/tvdb/%s?type=episode", tvdbId)

	params := napping.Params{}.AsUrlValues()

	cacheStore := cache.NewFileStore(path.Join(config.Get().ProfilePath, "cache"))
	key := fmt.Sprintf("com.trakt.episode.tvdb.%s", tvdbId)
	if err := cacheStore.Get(key, &episode); err != nil {
		resp, err := Get(endPoint, params)
		if err != nil {
			log.Error(err)
			xbmc.Notify("Quasar", "Failed getting Trakt episode using TVDB ID, check your logs.", config.AddonIcon())
			return
		}
		if err := resp.Unmarshal(&episode); err != nil {
			log.Warning(err)
		}
		cacheStore.Set(key, episode, cacheExpiration)
	}
	return
}

// TODO Actually use this somewhere
func SearchShows(query string, page string) (shows []*Shows, err error) {
	endPoint := "search"

	params := napping.Params{
		"page": page,
		"limit": strconv.Itoa(config.Get().ResultsPerPage),
		"query": query,
		"extended": "full,images",
	}.AsUrlValues()

	resp, err := Get(endPoint, params)

	if err != nil {
		return shows, err
	} else if resp.Status() != 200 {
		log.Error(err)
		return shows, errors.New(fmt.Sprintf("Bad status searching Trakt shows: %d", resp.Status()))
	}

	if err := resp.Unmarshal(&shows); err != nil {
		log.Warning(err)
	}
	shows = setShowsFanart(shows)

	return shows, err
}

func TopShows(topCategory string, page string) (shows []*Shows, err error) {
	endPoint := "shows/" + topCategory

	resultsPerPage := config.Get().ResultsPerPage
	limit := resultsPerPage * PagesAtOnce
	pageInt, err := strconv.Atoi(page)
	if err != nil {
		return
	}
	page = strconv.Itoa((pageInt - 1) * resultsPerPage / limit + 1)
	params := napping.Params{
		"page": page,
		"limit": strconv.Itoa(limit),
		"extended": "full,images",
	}.AsUrlValues()

	cacheStore := cache.NewFileStore(path.Join(config.Get().ProfilePath, "cache"))
	key := fmt.Sprintf("com.trakt.shows.%s.%s", topCategory, page)
	if err := cacheStore.Get(key, &shows); err != nil {
		resp, err := Get(endPoint, params)

		if err != nil {
			return shows, err
		} else if resp.Status() != 200 {
			return shows, errors.New(fmt.Sprintf("Bad status getting top %s Trakt shows: %d", topCategory, resp.Status()))
		}

	  if topCategory == "popular" {
			var showList []*Show
			if err := resp.Unmarshal(&showList); err != nil {
				return shows, err
			}

			showListing := make([]*Shows, 0)
			for _, show := range showList {
				showItem := Shows{
					Show: show,
				}
				showListing = append(showListing, &showItem)
			}
			shows = showListing
	  } else {
			if err := resp.Unmarshal(&shows); err != nil {
				log.Warning(err)
			}
	  }

		if page != "0" {
			shows = setShowsFanart(shows)
		}

		cacheStore.Set(key, shows, recentExpiration)
	}

	return shows, err
}

func WatchlistShows() (shows []*Shows, err error) {
	if err := Authorized(); err != nil {
		return shows, err
	}

	endPoint := "sync/watchlist/shows"

	params := napping.Params{
		"extended": "full,images",
	}.AsUrlValues()

	cacheStore := cache.NewFileStore(path.Join(config.Get().ProfilePath, "cache"))
	key := "com.trakt.shows.watchlist"
	if err := cacheStore.Get(key, &shows); err != nil {
		resp, err := GetWithAuth(endPoint, params)

		if err != nil {
			return shows, err
		} else if resp.Status() != 200 {
			log.Error(err)
			return shows, errors.New(fmt.Sprintf("Bad status getting Trakt watchlist for shows: %d", resp.Status()))
		}

		var watchlist []*WatchlistShow
		if err := resp.Unmarshal(&watchlist); err != nil {
			log.Warning(err)
		}

		showListing := make([]*Shows, 0)
		for _, show := range watchlist {
			showItem := Shows{
				Show: show.Show,
			}
			showListing = append(showListing, &showItem)
		}
		shows = showListing

		shows = setShowsFanart(shows)

		cacheStore.Set(key, shows, 1 * time.Minute)
	}

	return shows, err
}

func CollectionShows() (shows []*Shows, err error) {
	if err := Authorized(); err != nil {
		return shows, err
	}

	endPoint := "sync/collection/shows"

	params := napping.Params{
		"extended": "full,images",
	}.AsUrlValues()

	cacheStore := cache.NewFileStore(path.Join(config.Get().ProfilePath, "cache"))
	key := "com.trakt.shows.collection"
	if err := cacheStore.Get(key, &shows); err != nil {
		resp, err := GetWithAuth(endPoint, params)

		if err != nil {
			return shows, err
		} else if resp.Status() != 200 {
			return shows, errors.New(fmt.Sprintf("Bad status getting Trakt collection for shows: %d", resp.Status()))
		}

		var collection []*WatchlistShow
		if err := resp.Unmarshal(&collection); err != nil {
			log.Warning(err)
		}

		showListing := make([]*Shows, 0)
		for _, show := range collection {
			showItem := Shows{
				Show: show.Show,
			}
			showListing = append(showListing, &showItem)
		}
		shows = showListing

		shows = setShowsFanart(shows)

		cacheStore.Set(key, shows, 1 * time.Minute)
	}

	return shows, err
}

func ListItemsShows(listId string, page string) (shows []*Shows, err error) {
	endPoint := fmt.Sprintf("users/%s/lists/%s/items/shows", config.Get().TraktUsername, listId)

	params := napping.Params{}.AsUrlValues()

	if page != "0" {
		params = napping.Params{
			"page": page,
			"limit": strconv.Itoa(config.Get().ResultsPerPage),
			"extended": "full,images",
		}.AsUrlValues()
	}

	var resp *napping.Response

	cacheStore := cache.NewFileStore(path.Join(config.Get().ProfilePath, "cache"))
	key := fmt.Sprintf("com.trakt.shows.list.%s", listId)
	if err := cacheStore.Get(key, &shows); err != nil {
		if erra := Authorized(); erra != nil {
			resp, err = Get(endPoint, params)
		} else {
			resp, err = GetWithAuth(endPoint, params)
		}

		if err != nil || resp.Status() != 200 {
			return shows, err
		}

		var list []*ListItem
		if err := resp.Unmarshal(&list); err != nil {
			log.Warning(err)
		}

		showListing := make([]*Shows, 0)
		for _, show := range list {
			showItem := Shows{
				Show: show.Show,
			}
			showListing = append(showListing, &showItem)
		}
		shows = showListing

		if page != "0" {
			shows = setShowsFanart(shows)
		}

		cacheStore.Set(key, shows, 1 * time.Minute)
	}

	return shows, err
}

func (show *Show) ToListItem() *xbmc.ListItem {
	return &xbmc.ListItem{
		Label: show.Title,
		Info: &xbmc.ListItemInfo{
			Count:         rand.Int(),
			Title:         show.Title,
			OriginalTitle: show.Title,
			Year:          show.Year,
			Genre:         strings.Title(strings.Join(show.Genres, " / ")),
			Plot:          show.Overview,
			PlotOutline:   show.Overview,
			Rating:        show.Rating,
			Votes:         strconv.Itoa(show.Votes),
			Duration:      show.Runtime * 60,
			MPAA:          show.Certification,
			Code:          show.IDs.IMDB,
			IMDBNumber:    show.IDs.IMDB,
			Trailer:       show.Trailer,
			DBTYPE:        "tvshow",
			Mediatype:     "tvshow",
		},
		Art: &xbmc.ListItemArt{
			Poster: show.Images.Poster.Full,
			FanArt: show.Images.FanArt.Full,
			Banner: show.Images.Banner.Full,
			Thumbnail: show.Images.Thumbnail.Full,
		},
	}
}

func (season *Season) ToListItem(show *Show) *xbmc.ListItem {
	seasonLabel := fmt.Sprintf("Season %d", season.Number)
	return &xbmc.ListItem{
		Label: seasonLabel,
		Info: &xbmc.ListItemInfo{
			Count:         rand.Int(),
			Title:         seasonLabel,
			OriginalTitle: seasonLabel,
			Season:        season.Number,
			Rating:        season.Rating,
			Votes:         strconv.Itoa(season.Votes),
			Code:          show.IDs.IMDB,
			IMDBNumber:    show.IDs.IMDB,
			DBTYPE:        "season",
			Mediatype:     "season",
		},
		Art: &xbmc.ListItemArt{
			Poster: season.Images.Poster.Full,
			Thumbnail: season.Images.Thumbnail.Full,
			// FanArt: season.Images.FanArt.Full,
		},
	}
}

func (episode *Episode) ToListItem(show *Show) *xbmc.ListItem {
	title := fmt.Sprintf("%dx%02d %s", episode.Season, episode.Number, episode.Title)
	return &xbmc.ListItem{
		Label:     title,
		Thumbnail: episode.Images.ScreenShot.Full,
		Info: &xbmc.ListItemInfo{
			Count:         rand.Int(),
			Title:         title,
			OriginalTitle: episode.Title,
			Plot:          episode.Overview,
			PlotOutline:   episode.Overview,
			Rating:        episode.Rating,
			Votes:         strconv.Itoa(episode.Votes),
			Episode:       episode.Number,
			Season:        episode.Season,
			Code:          show.IDs.IMDB,
			IMDBNumber:    show.IDs.IMDB,
			DBTYPE:        "episode",
			Mediatype:     "episode",
		},
		Art: &xbmc.ListItemArt{
			Thumbnail: episode.Images.ScreenShot.Full,
			// FanArt:    episode.Season.Show.Images.FanArt.Full,
			// Banner:    episode.Season.Show.Images.Banner.Full,
		},
	}
}
