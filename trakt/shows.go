package trakt

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/elgatito/elementum/cache"
	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/playcount"
	"github.com/elgatito/elementum/tmdb"
	"github.com/elgatito/elementum/util"
	"github.com/elgatito/elementum/xbmc"
	"github.com/jmcvetta/napping"
)

// Fill fanart from TMDB
func setShowFanart(show *Show) *Show {
	if show.Images == nil {
		show.Images = &Images{}
	}
	if show.Images.Poster == nil {
		show.Images.Poster = &Sizes{}
	}
	if show.Images.Thumbnail == nil {
		show.Images.Thumbnail = &Sizes{}
	}
	if show.Images.FanArt == nil {
		show.Images.FanArt = &Sizes{}
	}
	if show.Images.Banner == nil {
		show.Images.Banner = &Sizes{}
	}

	if show.IDs == nil || show.IDs.TMDB == 0 {
		return show
	}

	tmdbImages := tmdb.GetShowImages(show.IDs.TMDB)
	if tmdbImages == nil {
		return show
	}

	if len(tmdbImages.Posters) > 0 {
		posterImage := tmdb.ImageURL(tmdbImages.Posters[0].FilePath, "w500")
		for _, image := range tmdbImages.Posters {
			if image.Iso639_1 == config.Get().Language {
				posterImage = tmdb.ImageURL(image.FilePath, "w500")
			}
		}
		show.Images.Poster.Full = posterImage
		show.Images.Thumbnail.Full = posterImage
	}
	if len(tmdbImages.Backdrops) > 0 {
		backdropImage := tmdb.ImageURL(tmdbImages.Backdrops[0].FilePath, "w1280")
		for _, image := range tmdbImages.Backdrops {
			if image.Iso639_1 == config.Get().Language {
				backdropImage = tmdb.ImageURL(image.FilePath, "w1280")
			}
		}
		show.Images.FanArt.Full = backdropImage
		show.Images.Banner.Full = backdropImage
	}
	return show
}

func setShowsFanart(shows []*Shows) []*Shows {
	// TODO: Remove when finish with debugging
	if len(shows) > 30 {
		return shows
	}

	wg := sync.WaitGroup{}
	for i, show := range shows {
		wg.Add(1)
		go func(idx int, s *Shows) {
			defer wg.Done()
			shows[idx].Show = setShowFanart(s.Show)
		}(i, show)
	}
	wg.Wait()

	return shows
}

func setProgressShowsFanart(shows []*ProgressShow) []*ProgressShow {
	wg := sync.WaitGroup{}
	for i, show := range shows {
		wg.Add(1)
		go func(idx int, s *ProgressShow) {
			defer wg.Done()
			if s != nil && s.Show != nil {
				shows[idx].Show = setShowFanart(s.Show)
			}
		}(i, show)
	}
	wg.Wait()
	return shows
}

func setCalendarShowsFanart(shows []*CalendarShow) []*CalendarShow {
	// TODO: Remove when finish with debugging
	if len(shows) > 30 {
		return shows
	}

	wg := sync.WaitGroup{}
	for i, show := range shows {
		wg.Add(1)
		go func(idx int, s *CalendarShow) {
			defer wg.Done()
			shows[idx].Show = setShowFanart(s.Show)
		}(i, show)
	}
	wg.Wait()

	return shows
}

// GetShow ...
func GetShow(ID string) (show *Show) {
	endPoint := fmt.Sprintf("shows/%s", ID)

	params := napping.Params{
		"extended": "full,images",
	}.AsUrlValues()

	cacheStore := cache.NewDBStore()
	key := fmt.Sprintf("com.trakt.show.%s", ID)
	if err := cacheStore.Get(key, &show); err != nil {
		resp, err := Get(endPoint, params)
		if err != nil {
			log.Error(err)
			xbmc.Notify("Elementum", fmt.Sprintf("Failed getting Trakt show (%s), check your logs.", ID), config.AddonIcon())
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

// GetShowByTMDB ...
func GetShowByTMDB(tmdbID string) (show *Show) {
	endPoint := fmt.Sprintf("search/tmdb/%s?type=show", tmdbID)

	params := napping.Params{}.AsUrlValues()

	cacheStore := cache.NewDBStore()
	key := fmt.Sprintf("com.trakt.show.tmdb.%s", tmdbID)
	if err := cacheStore.Get(key, &show); err != nil {
		resp, err := Get(endPoint, params)
		if err != nil {
			log.Error(err)
			xbmc.Notify("Elementum", "Failed getting Trakt show using TMDB ID, check your logs.", config.AddonIcon())
			return
		}

		var results ShowSearchResults
		if err := resp.Unmarshal(&results); err != nil {
			log.Warning(err)
		}
		if results != nil && len(results) > 0 && results[0].Show != nil {
			show = results[0].Show
		}
		cacheStore.Set(key, show, cacheExpiration)
	}
	return
}

// GetShowByTVDB ...
func GetShowByTVDB(tvdbID string) (show *Show) {
	endPoint := fmt.Sprintf("search/tvdb/%s?type=show", tvdbID)

	params := napping.Params{}.AsUrlValues()

	cacheStore := cache.NewDBStore()
	key := fmt.Sprintf("com.trakt.show.tvdb.%s", tvdbID)
	if err := cacheStore.Get(key, &show); err != nil {
		resp, err := Get(endPoint, params)
		if err != nil {
			log.Error(err)
			xbmc.Notify("Elementum", "Failed getting Trakt show using TVDB ID, check your logs.", config.AddonIcon())
			return
		}
		if err := resp.Unmarshal(&show); err != nil {
			log.Warning(err)
		}
		cacheStore.Set(key, show, cacheExpiration)
	}
	return
}

// GetEpisode ...
func GetEpisode(id string) (episode *Episode) {
	endPoint := fmt.Sprintf("search/trakt/%s?type=episode", id)

	params := napping.Params{}.AsUrlValues()

	cacheStore := cache.NewDBStore()
	key := fmt.Sprintf("com.trakt.episode.%s", id)
	if err := cacheStore.Get(key, &episode); err != nil {
		resp, err := Get(endPoint, params)
		if err != nil {
			log.Error(err)
			xbmc.Notify("Elementum", "Failed getting Trakt episode, check your logs.", config.AddonIcon())
			return
		}
		if err := resp.Unmarshal(&episode); err != nil {
			log.Warning(err)
		}
		cacheStore.Set(key, episode, cacheExpiration)
	}
	return
}

// GetEpisodeByTMDB ...
func GetEpisodeByTMDB(tmdbID string) (episode *Episode) {
	endPoint := fmt.Sprintf("search/tmdb/%s?type=episode", tmdbID)

	params := napping.Params{}.AsUrlValues()

	cacheStore := cache.NewDBStore()
	key := fmt.Sprintf("com.trakt.episode.tmdb.%s", tmdbID)
	if err := cacheStore.Get(key, &episode); err != nil {
		resp, err := Get(endPoint, params)
		if err != nil {
			log.Error(err)
			xbmc.Notify("Elementum", "Failed getting Trakt episode using TMDB ID, check your logs.", config.AddonIcon())
			return
		}

		var results EpisodeSearchResults
		if err := resp.Unmarshal(&results); err != nil {
			log.Warning(err)
		}
		if results != nil && len(results) > 0 && results[0].Episode != nil {
			episode = results[0].Episode
		}
		cacheStore.Set(key, episode, cacheExpiration)
	}
	return
}

// GetEpisodeByTVDB ...
func GetEpisodeByTVDB(tvdbID string) (episode *Episode) {
	endPoint := fmt.Sprintf("search/tvdb/%s?type=episode", tvdbID)

	params := napping.Params{}.AsUrlValues()

	cacheStore := cache.NewDBStore()
	key := fmt.Sprintf("com.trakt.episode.tvdb.%s", tvdbID)
	if err := cacheStore.Get(key, &episode); err != nil {
		resp, err := Get(endPoint, params)
		if err != nil {
			log.Error(err)
			xbmc.Notify("Elementum", "Failed getting Trakt episode using TVDB ID, check your logs.", config.AddonIcon())
			return
		}
		if err := resp.Unmarshal(&episode); err != nil {
			log.Warning(err)
		}
		cacheStore.Set(key, episode, cacheExpiration)
	}
	return
}

// SearchShows ...
// TODO Actually use this somewhere
func SearchShows(query string, page string) (shows []*Shows, err error) {
	endPoint := "search"

	params := napping.Params{
		"page":     page,
		"limit":    strconv.Itoa(config.Get().ResultsPerPage),
		"query":    query,
		"extended": "full,images",
	}.AsUrlValues()

	resp, err := Get(endPoint, params)

	if err != nil {
		return
	} else if resp.Status() != 200 {
		log.Error(err)
		return shows, fmt.Errorf("Bad status searching Trakt shows: %d", resp.Status())
	}

	if err := resp.Unmarshal(&shows); err != nil {
		log.Warning(err)
	}
	shows = setShowsFanart(shows)

	return
}

// TopShows ...
func TopShows(topCategory string, page string) (shows []*Shows, total int, err error) {
	endPoint := "shows/" + topCategory

	resultsPerPage := config.Get().ResultsPerPage
	limit := resultsPerPage * PagesAtOnce
	pageInt, err := strconv.Atoi(page)
	if err != nil {
		return shows, 0, err
	}
	page = strconv.Itoa((pageInt-1)*resultsPerPage/limit + 1)
	params := napping.Params{
		"page":     page,
		"limit":    strconv.Itoa(limit),
		"extended": "full,images",
	}.AsUrlValues()

	cacheStore := cache.NewDBStore()
	key := fmt.Sprintf("com.trakt.shows.%s.%s", topCategory, page)
	totalKey := fmt.Sprintf("com.trakt.shows.%s.total", topCategory)
	if err := cacheStore.Get(key, &shows); err != nil {
		resp, err := Get(endPoint, params)

		if err != nil {
			return shows, 0, err
		} else if resp.Status() != 200 {
			return shows, 0, fmt.Errorf("Bad status getting top %s Trakt shows: %d", topCategory, resp.Status())
		}

		if topCategory == "popular" {
			var showList []*Show
			if errUnm := resp.Unmarshal(&showList); errUnm != nil {
				return shows, 0, errUnm
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
			if errUnm := resp.Unmarshal(&shows); errUnm != nil {
				log.Warning(errUnm)
			}
		}

		if page != "0" {
			shows = setShowsFanart(shows)
		}

		total, err = totalFromHeaders(resp.HttpResponse().Header)
		if err != nil {
			log.Warning(err)
		} else {
			cacheStore.Set(totalKey, total, recentExpiration)
		}

		cacheStore.Set(key, shows, recentExpiration)
	} else {
		if err := cacheStore.Get(totalKey, &total); err != nil {
			total = -1
		}
	}

	return
}

// WatchlistShows ...
func WatchlistShows() (shows []*Shows, err error) {
	if err := Authorized(); err != nil {
		return shows, err
	}

	endPoint := "sync/watchlist/shows"

	params := napping.Params{
		"extended": "full,images",
	}.AsUrlValues()

	cacheStore := cache.NewDBStore()
	key := "com.trakt.shows.watchlist"
	if err := cacheStore.Get(key, &shows); err != nil {
		resp, err := GetWithAuth(endPoint, params)

		if err != nil {
			return shows, err
		} else if resp.Status() != 200 {
			log.Error(err)
			return shows, fmt.Errorf("Bad status getting Trakt watchlist for shows: %d", resp.Status())
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

		cacheStore.Set(key, shows, 1*time.Minute)
	}

	return
}

// CollectionShows ...
func CollectionShows() (shows []*Shows, err error) {
	if err := Authorized(); err != nil {
		return shows, err
	}

	endPoint := "sync/collection/shows"

	params := napping.Params{
		"extended": "full,images",
	}.AsUrlValues()

	cacheStore := cache.NewDBStore()
	key := "com.trakt.shows.collection"
	if err := cacheStore.Get(key, &shows); err != nil {
		resp, err := GetWithAuth(endPoint, params)

		if err != nil {
			return shows, err
		} else if resp.Status() != 200 {
			return shows, fmt.Errorf("Bad status getting Trakt collection for shows: %d", resp.Status())
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

		cacheStore.Set(key, shows, 1*time.Minute)
	}

	return
}

// ListItemsShows ...
func ListItemsShows(listID string, withImages bool) (shows []*Shows, err error) {
	endPoint := fmt.Sprintf("users/%s/lists/%s/items/shows", config.Get().TraktUsername, listID)

	params := napping.Params{}.AsUrlValues()

	var resp *napping.Response

	cacheStore := cache.NewDBStore()
	full := ""
	if withImages {
		full = ".full"
	}
	key := fmt.Sprintf("com.trakt.shows.list.%s%s", listID, full)
	if errGet := cacheStore.Get(key, &shows); errGet != nil {
		if erra := Authorized(); erra != nil {
			resp, errGet = Get(endPoint, params)
		} else {
			resp, errGet = GetWithAuth(endPoint, params)
		}

		if errGet != nil || resp.Status() != 200 {
			return shows, errGet
		}

		var list []*ListItem
		if err = resp.Unmarshal(&list); err != nil {
			log.Warning(err)
		}

		showListing := make([]*Shows, 0)
		for _, show := range list {
			if show.Show == nil {
				continue
			}
			showItem := Shows{
				Show: show.Show,
			}
			showListing = append(showListing, &showItem)
		}
		shows = showListing

		if withImages {
			shows = setShowsFanart(shows)
		}

		cacheStore.Set(key, shows, 1*time.Minute)
	}

	return shows, err
}

// CalendarShows ...
func CalendarShows(endPoint string, page string) (shows []*CalendarShow, total int, err error) {
	resultsPerPage := config.Get().ResultsPerPage
	limit := resultsPerPage * PagesAtOnce
	pageInt, err := strconv.Atoi(page)
	if err != nil {
		return shows, 0, err
	}
	page = strconv.Itoa((pageInt-1)*resultsPerPage/limit + 1)
	params := napping.Params{
		"page":     page,
		"limit":    strconv.Itoa(limit),
		"extended": "full,images",
	}.AsUrlValues()

	cacheStore := cache.NewDBStore()
	endPointKey := strings.Replace(endPoint, "/", ".", -1)
	key := fmt.Sprintf("com.trakt.myshows.%s.%s", endPointKey, page)
	totalKey := fmt.Sprintf("com.trakt.myshows.%s.total", endPointKey)
	if err := cacheStore.Get(key, &shows); err != nil {
		resp, err := GetWithAuth("calendars/"+endPoint, params)

		if err != nil {
			return shows, 0, err
		} else if resp.Status() != 200 {
			return shows, 0, fmt.Errorf("Bad status getting %s Trakt shows: %d", endPoint, resp.Status())
		}

		if errUnm := resp.Unmarshal(&shows); errUnm != nil {
			log.Warning(errUnm)
		}

		if page != "0" {
			shows = setCalendarShowsFanart(shows)
		}

		total, err = totalFromHeaders(resp.HttpResponse().Header)
		if err != nil {
			total = -1
		} else {
			cacheStore.Set(totalKey, total, recentExpiration)
		}

		cacheStore.Set(key, shows, recentExpiration)
	} else {
		if err := cacheStore.Get(totalKey, &total); err != nil {
			total = -1
		}
	}

	return
}

// ToListItem ...
func (show *Show) ToListItem() *xbmc.ListItem {
	li := &xbmc.ListItem{
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
			Trailer:       util.TrailerURL(show.Trailer),
			PlayCount:     playcount.GetWatchedShowByTMDB(show.IDs.TMDB).Int(),
			DBTYPE:        "tvshow",
			Mediatype:     "tvshow",
		},
		Art: &xbmc.ListItemArt{},
	}

	if show.Images != nil {
		if show.Images.Poster != nil {
			li.Art.Poster = show.Images.Poster.Full
			li.Art.TvShowPoster = show.Images.Poster.Full
		}
		if show.Images.FanArt != nil {
			li.Art.FanArt = show.Images.FanArt.Full
		}
		if show.Images.Banner != nil {
			li.Art.Banner = show.Images.Banner.Full
		}
		if show.Images.Thumbnail != nil {
			li.Art.Thumbnail = show.Images.Thumbnail.Full
		}
		if show.Images.ClearArt != nil {
			li.Art.ClearArt = show.Images.ClearArt.Full
		}
	}

	return li
}

// WatchedShows ...
func WatchedShows() (shows []*WatchedShow, err error) {
	if err := Authorized(); err != nil {
		return shows, nil
	}

	endPoint := "sync/watched/shows"

	params := napping.Params{
		"extended": "full,images",
	}.AsUrlValues()

	cacheStore := cache.NewDBStore()
	key := "com.trakt.episodes.watched"
	if err := cacheStore.Get(key, &shows); err != nil {
		resp, err := GetWithAuth(endPoint, params)

		if err != nil {
			return shows, err
		} else if resp.Status() != 200 {
			return shows, fmt.Errorf("Bad status getting Trakt watched for shows: %d", resp.Status())
		}

		if err := resp.Unmarshal(&shows); err != nil {
			log.Warning(err)
		}

		cacheStore.Set(key, shows, watchedExpiration)
	}

	return
}

// WatchedShowsProgress ...
func WatchedShowsProgress() (shows []*ProgressShow, err error) {
	if errAuth := Authorized(); errAuth != nil {
		return nil, errAuth
	}

	watchedShows, errWatched := WatchedShows()
	if errWatched != nil {
		log.Errorf("Error getting the watched shows: %v", errWatched)
		return nil, errWatched
	}

	params := napping.Params{
		"hidden":         "false",
		"specials":       "false",
		"count_specials": "false",
	}.AsUrlValues()

	showsList := make([]*ProgressShow, len(watchedShows))
	watchedProgressShows := make([]*WatchedProgressShow, len(watchedShows))

	var wg sync.WaitGroup
	wg.Add(len(watchedShows))
	for i, show := range watchedShows {
		go func(i int, show *WatchedShow) {
			defer wg.Done()
			endPoint := fmt.Sprintf("shows/%d/progress/watched", show.Show.IDs.Trakt)

			resp, err := GetWithAuth(endPoint, params)
			if err != nil {
				log.Errorf("Error getting endpoint %s for show '%d': %#v", endPoint, show.Show.IDs.Trakt, err)
				return
			} else if resp.Status() != 200 {
				log.Errorf("Got %d response status getting endpoint %s for show '%d'", resp.Status(), endPoint, show.Show.IDs.Trakt)
				return
			}
			var watchedProgressShow *WatchedProgressShow
			if err := resp.Unmarshal(&watchedProgressShow); err != nil {
				log.Warningf("Can't unmarshal response: %#v", err)
			}

			watchedProgressShows[i] = watchedProgressShow

			if watchedProgressShow.NextEpisode != nil && watchedProgressShow.NextEpisode.Number != 0 && watchedProgressShow.NextEpisode.Season != 0 {
				showsList[i] = &ProgressShow{
					Show:    show.Show,
					Episode: watchedProgressShow.NextEpisode,
				}
			}
		}(i, show)
	}
	wg.Wait()

	for _, s := range showsList {
		if s != nil {
			shows = append(shows, s)
		}
	}

	shows = setProgressShowsFanart(shows)

	return
}

// ToListItem ...
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
			PlayCount:     playcount.GetWatchedSeasonByTMDB(show.IDs.TMDB, season.Number).Int(),
			DBTYPE:        "season",
			Mediatype:     "season",
		},
		Art: &xbmc.ListItemArt{
			Poster:    season.Images.Poster.Full,
			Thumbnail: season.Images.Thumbnail.Full,
			// FanArt: season.Images.FanArt.Full,
		},
	}
}

// ToListItem ...
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
			PlayCount:     playcount.GetWatchedEpisodeByTMDB(show.IDs.TMDB, episode.Season, episode.Number).Int(),
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
