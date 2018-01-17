package tmdb

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/elgatito/elementum/cache"
	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/playcount"
	"github.com/elgatito/elementum/util"
	"github.com/elgatito/elementum/xbmc"
	"github.com/jmcvetta/napping"
)

// LogError ...
func LogError(err error) {
	if err != nil {
		pc, fn, line, _ := runtime.Caller(1)
		log.Errorf("in %s[%s:%d] %#v: %v)", runtime.FuncForPC(pc).Name(), fn, line, err, err)
	}
}

// GetShowImages ...
func GetShowImages(showID int) *Images {
	var images *Images
	cacheStore := cache.NewDBStore()
	key := fmt.Sprintf("com.tmdb.show.%d.images", showID)
	if err := cacheStore.Get(key, &images); err != nil {
		rl.Call(func() error {
			urlValues := napping.Params{
				"api_key":                apiKey,
				"include_image_language": fmt.Sprintf("%s,en,null", config.Get().Language),
			}.AsUrlValues()
			resp, err := napping.Get(
				tmdbEndpoint+"tv/"+strconv.Itoa(showID)+"/images",
				&urlValues,
				&images,
				nil,
			)
			if err != nil {
				log.Error(err)
				xbmc.Notify("Elementum", "Failed getting images, check your logs.", config.AddonIcon())
			} else if resp.Status() == 429 {
				log.Warningf("Rate limit exceeded getting images for %d, cooling down...", showID)
				rl.CoolDown(resp.HttpResponse().Header)
				return util.ErrExceeded
			} else if resp.Status() != 200 {
				log.Warningf("Bad status getting images for %d: %d", showID, resp.Status())
			}
			if images != nil {
				cacheStore.Set(key, images, imagesCacheExpiration)
			}

			return nil
		})
	}
	return images
}

// GetShowByID ...
func GetShowByID(tmdbID string, language string) *Show {
	id, _ := strconv.Atoi(tmdbID)
	return GetShow(id, language)
}

// GetShow ...
func GetShow(showID int, language string) (show *Show) {
	if showID == 0 {
		return
	}
	cacheStore := cache.NewDBStore()
	key := fmt.Sprintf("com.tmdb.show.%d.%s", showID, language)
	if err := cacheStore.Get(key, &show); err != nil {
		rl.Call(func() error {
			urlValues := napping.Params{
				"api_key":            apiKey,
				"append_to_response": "credits,images,alternative_titles,translations,external_ids",
				"language":           language,
			}.AsUrlValues()
			resp, err := napping.Get(
				tmdbEndpoint+"tv/"+strconv.Itoa(showID),
				&urlValues,
				&show,
				nil,
			)
			if err != nil {
				switch e := err.(type) {
				case *json.UnmarshalTypeError:
					log.Errorf("UnmarshalTypeError: Value[%s] Type[%v] Offset[%d] for %d", e.Value, e.Type, e.Offset, showID)
				case *json.InvalidUnmarshalError:
					log.Errorf("InvalidUnmarshalError: Type[%v]", e.Type)
				default:
					log.Error(err)
				}
				LogError(err)
				xbmc.Notify("Elementum", "Failed getting show, check your logs.", config.AddonIcon())
			} else if resp.Status() == 429 {
				log.Warningf("Rate limit exceeded getting show %d, cooling down...", showID)
				rl.CoolDown(resp.HttpResponse().Header)
				return util.ErrExceeded
			} else if resp.Status() != 200 {
				message := fmt.Sprintf("Bad status getting show for %d: %d", showID, resp.Status())
				log.Warning(message)
				xbmc.Notify("Elementum", message, config.AddonIcon())
			}

			if show != nil {
				cacheStore.Set(key, show, cacheExpiration)
			}

			return nil
		})
	}
	if show == nil {
		return nil
	}

	switch t := show.RawPopularity.(type) {
	case string:
		if popularity, err := strconv.ParseFloat(t, 64); err == nil {
			show.Popularity = popularity
		}
	case float64:
		show.Popularity = t
	}

	return show
}

// GetShows ...
func GetShows(showIds []int, language string) Shows {
	var wg sync.WaitGroup
	shows := make(Shows, len(showIds))
	wg.Add(len(showIds))
	for i, showID := range showIds {
		go func(i int, showId int) {
			defer wg.Done()
			shows[i] = GetShow(showId, language)
		}(i, showID)
	}
	wg.Wait()
	return shows
}

// SearchShows ...
func SearchShows(query string, language string, page int) (Shows, int) {
	var results EntityList
	rl.Call(func() error {
		urlValues := napping.Params{
			"api_key": apiKey,
			"query":   query,
			"page":    strconv.Itoa(page),
		}.AsUrlValues()
		resp, err := napping.Get(
			tmdbEndpoint+"search/tv",
			&urlValues,
			&results,
			nil,
		)
		if err != nil {
			log.Error(err)
			xbmc.Notify("Elementum", "Failed searching shows check your logs.", config.AddonIcon())
		} else if resp.Status() == 429 {
			log.Warningf("Rate limit exceeded searching shows for %s, cooling down...", query)
			rl.CoolDown(resp.HttpResponse().Header)
			return util.ErrExceeded
		} else if resp.Status() != 200 {
			message := fmt.Sprintf("Bad status searching shows: %d", resp.Status())
			log.Error(message)
			xbmc.Notify("Elementum", message, config.AddonIcon())
		}

		return nil
	})
	tmdbIds := make([]int, 0, len(results.Results))
	for _, entity := range results.Results {
		tmdbIds = append(tmdbIds, entity.ID)
	}
	return GetShows(tmdbIds, language), results.TotalResults
}

func listShows(endpoint string, cacheKey string, params napping.Params, page int) (Shows, int) {
	params["api_key"] = apiKey
	totalResults := -1
	genre := params["with_genres"]
	if params["with_genres"] == "" {
		genre = "all"
	}
	limit := ResultsPerPage * PagesAtOnce
	pageGroup := (page-1)*ResultsPerPage/limit + 1

	shows := make(Shows, limit)

	cacheStore := cache.NewDBStore()
	key := fmt.Sprintf("com.tmdb.topshows.%s.%s.%d", cacheKey, genre, pageGroup)
	totalKey := fmt.Sprintf("com.tmdb.topshows.%s.%s.total", cacheKey, genre)
	if err := cacheStore.Get(key, &shows); err != nil {
		wg := sync.WaitGroup{}
		for p := 0; p < PagesAtOnce; p++ {
			wg.Add(1)
			currentPage := (pageGroup-1)*ResultsPerPage + p + 1
			go func(p int) {
				defer wg.Done()
				var results *EntityList
				pageParams := napping.Params{
					"page": strconv.Itoa(currentPage),
				}
				for k, v := range params {
					pageParams[k] = v
				}
				urlParams := pageParams.AsUrlValues()
				rl.Call(func() error {
					resp, err := napping.Get(
						tmdbEndpoint+endpoint,
						&urlParams,
						&results,
						nil,
					)
					if err != nil {
						log.Error(err)
						xbmc.Notify("Elementum", "Failed while listing shows, check your logs.", config.AddonIcon())
					} else if resp.Status() == 429 {
						log.Warningf("Rate limit exceeded while listing shows from %s, cooling down...", endpoint)
						rl.CoolDown(resp.HttpResponse().Header)
						return util.ErrExceeded
					} else if resp.Status() != 200 {
						message := fmt.Sprintf("Bad status while listing shows: %d", resp.Status())
						log.Error(message)
						xbmc.Notify("Elementum", message, config.AddonIcon())
					}

					return nil
				})
				if results != nil {
					if totalResults == -1 {
						totalResults = results.TotalResults
						cacheStore.Set(totalKey, totalResults, recentExpiration)
					}

					var wgItems sync.WaitGroup
					wgItems.Add(len(results.Results))
					for s, show := range results.Results {
						if show == nil {
							wgItems.Done()
							continue
						}

						go func(i int, tmdbId int) {
							defer wgItems.Done()
							shows[i] = GetShow(tmdbId, params["language"])
						}(p*ResultsPerPage+s, show.ID)
					}
					wgItems.Wait()
				}
			}(p)
		}
		wg.Wait()
		cacheStore.Set(key, shows, recentExpiration)
	} else {
		if err := cacheStore.Get(totalKey, &totalResults); err != nil {
			totalResults = -1
		}
	}
	return shows, totalResults
}

// PopularShows ...
func PopularShows(genre string, language string, page int) (Shows, int) {
	var p napping.Params
	if genre == "" {
		p = napping.Params{
			"language":           language,
			"sort_by":            "popularity.desc",
			"first_air_date.lte": time.Now().UTC().Format("2006-01-02"),
		}
	} else {
		p = napping.Params{
			"language":           language,
			"sort_by":            "popularity.desc",
			"first_air_date.lte": time.Now().UTC().Format("2006-01-02"),
			"with_genres":        genre,
		}
	}
	return listShows("discover/tv", "popular", p, page)
}

// RecentShows ...
func RecentShows(genre string, language string, page int) (Shows, int) {
	var p napping.Params
	if genre == "" {
		p = napping.Params{
			"language":           language,
			"sort_by":            "first_air_date.desc",
			"first_air_date.lte": time.Now().UTC().Format("2006-01-02"),
		}
	} else {
		p = napping.Params{
			"language":           language,
			"sort_by":            "first_air_date.desc",
			"first_air_date.lte": time.Now().UTC().Format("2006-01-02"),
			"with_genres":        genre,
		}
	}
	return listShows("discover/tv", "recent.shows", p, page)
}

// RecentEpisodes ...
func RecentEpisodes(genre string, language string, page int) (Shows, int) {
	var p napping.Params

	if genre == "" {
		p = napping.Params{
			"language":           language,
			"air_date.gte":       time.Now().UTC().AddDate(0, 0, -3).Format("2006-01-02"),
			"first_air_date.lte": time.Now().UTC().Format("2006-01-02"),
		}
	} else {
		p = napping.Params{
			"language":           language,
			"air_date.gte":       time.Now().UTC().AddDate(0, 0, -3).Format("2006-01-02"),
			"first_air_date.lte": time.Now().UTC().Format("2006-01-02"),
			"with_genres":        genre,
		}
	}
	return listShows("discover/tv", "recent.episodes", p, page)
}

// TopRatedShows ...
func TopRatedShows(genre string, language string, page int) (Shows, int) {
	return listShows("tv/top_rated", "toprated", napping.Params{"language": language}, page)
}

// MostVotedShows ...
func MostVotedShows(genre string, language string, page int) (Shows, int) {
	return listShows("discover/tv", "mostvoted", napping.Params{
		"language":           language,
		"sort_by":            "vote_count.desc",
		"first_air_date.lte": time.Now().UTC().Format("2006-01-02"),
		"with_genres":        genre,
	}, page)
}

// GetTVGenres ...
func GetTVGenres(language string) []*Genre {
	genres := GenreList{}

	cacheStore := cache.NewDBStore()
	key := fmt.Sprintf("com.tmdb.genres.shows.%s", language)
	if err := cacheStore.Get(key, &genres); err != nil {
		rl.Call(func() error {
			urlValues := napping.Params{
				"api_key":  apiKey,
				"language": language,
			}.AsUrlValues()
			resp, err := napping.Get(
				tmdbEndpoint+"genre/tv/list",
				&urlValues,
				&genres,
				nil,
			)
			if err != nil {
				log.Error(err)
				xbmc.Notify("Elementum", "Failed getting TV genres, check your logs.", config.AddonIcon())
			} else if resp.Status() == 429 {
				log.Warning("Rate limit exceeded getting TV genres, cooling down...")
				rl.CoolDown(resp.HttpResponse().Header)
				return util.ErrExceeded
			} else if resp.Status() != 200 {
				message := fmt.Sprintf("Bad status getting TV genres: %d", resp.Status())
				log.Error(message)
				xbmc.Notify("Elementum", message, config.AddonIcon())
			}

			return nil
		})
		if genres.Genres != nil && len(genres.Genres) > 0 {
			cacheStore.Set(key, genres, cacheExpiration)
		}
	}
	return genres.Genres
}

// ToListItem ...
func (show *Show) ToListItem() *xbmc.ListItem {
	year, _ := strconv.Atoi(strings.Split(show.FirstAirDate, "-")[0])

	name := show.Name
	if config.Get().UseOriginalTitle && show.OriginalName != "" {
		name = show.OriginalName
	}

	item := &xbmc.ListItem{
		Label: name,
		Info: &xbmc.ListItemInfo{
			Year:          year,
			Count:         rand.Int(),
			Title:         name,
			OriginalTitle: show.OriginalName,
			Plot:          show.Overview,
			PlotOutline:   show.Overview,
			Code:          show.ExternalIDs.IMDBId,
			IMDBNumber:    show.ExternalIDs.IMDBId,
			Date:          show.FirstAirDate,
			Votes:         strconv.Itoa(show.VoteCount),
			Rating:        show.VoteAverage,
			TVShowTitle:   show.OriginalName,
			Premiered:     show.FirstAirDate,
			PlayCount:     playcount.GetWatchedShowByTMDB(show.ID).Int(),
			DBTYPE:        "tvshow",
			Mediatype:     "tvshow",
		},
		Art: &xbmc.ListItemArt{
			FanArt: ImageURL(show.BackdropPath, "w1280"),
			Poster: ImageURL(show.PosterPath, "w500"),
		},
	}

	item.Thumbnail = item.Art.Poster
	item.Art.Thumbnail = item.Art.Poster

	if show.InProduction {
		item.Info.Status = "Continuing"
	} else {
		item.Info.Status = "Discontinued"
	}

	genres := make([]string, 0, len(show.Genres))
	for _, genre := range show.Genres {
		genres = append(genres, genre.Name)
	}
	item.Info.Genre = strings.Join(genres, " / ")

	for _, company := range show.ProductionCompanies {
		item.Info.Studio = company.Name
		break
	}
	if show.Credits != nil {
		item.Info.CastAndRole = make([][]string, 0)
		for _, cast := range show.Credits.Cast {
			item.Info.CastAndRole = append(item.Info.CastAndRole, []string{cast.Name, cast.Character})
		}
		directors := make([]string, 0)
		writers := make([]string, 0)
		for _, crew := range show.Credits.Crew {
			switch crew.Job {
			case "Director":
				directors = append(directors, crew.Name)
			case "Writer":
				writers = append(writers, crew.Name)
			}
		}
		item.Info.Director = strings.Join(directors, " / ")
		item.Info.Writer = strings.Join(writers, " / ")
	}
	return item
}
