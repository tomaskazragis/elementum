package tmdb

import (
	"fmt"
	"math/rand"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/elgatito/elementum/cache"
	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/util"
	"github.com/elgatito/elementum/xbmc"

	"github.com/jmcvetta/napping"
)

// ByPopularity ...
type ByPopularity Movies

func (a ByPopularity) Len() int           { return len(a) }
func (a ByPopularity) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByPopularity) Less(i, j int) bool { return a[i].Popularity < a[j].Popularity }

// GetImages ...
func GetImages(movieID int) *Images {
	var images *Images
	cacheStore := cache.NewFileStore(path.Join(config.Get().ProfilePath, "cache"))
	key := fmt.Sprintf("com.tmdb.movie.%d.images", movieID)
	if err := cacheStore.Get(key, &images); err != nil {
		rl.Call(func() error {
			urlValues := napping.Params{
				"api_key":                apiKey,
				"include_image_language": fmt.Sprintf("%s,en,null", config.Get().Language),
			}.AsUrlValues()
			resp, err := napping.Get(
				tmdbEndpoint+"movie/"+strconv.Itoa(movieID)+"/images",
				&urlValues,
				&images,
				nil,
			)
			if err != nil {
				log.Error(err)
				xbmc.Notify("Elementum", fmt.Sprintf("Failed getting images for movie %d, check your logs.", movieID), config.AddonIcon())
			} else if resp.Status() == 429 {
				log.Warningf("Rate limit exceeded getting images for %d, cooling down...", movieID)
				rl.CoolDown(resp.HttpResponse().Header)
				return util.ErrExceeded
			} else if resp.Status() != 200 {
				log.Warningf("Bad status getting images for %d: %d", movieID, resp.Status())
			}
			if images != nil {
				cacheStore.Set(key, images, imagesCacheExpiration)
			}

			return nil
		})
	}
	return images
}

// GetMovie ...
func GetMovie(tmdbID int, language string) *Movie {
	return GetMovieByID(strconv.Itoa(tmdbID), language)
}

// GetMovieByID ...
func GetMovieByID(movieID string, language string) *Movie {
	var movie *Movie
	cacheStore := cache.NewFileStore(path.Join(config.Get().ProfilePath, "cache"))
	key := fmt.Sprintf("com.tmdb.movie.%s.%s", movieID, language)
	if err := cacheStore.Get(key, &movie); err != nil {
		rl.Call(func() error {
			urlValues := napping.Params{
				"api_key":            apiKey,
				"append_to_response": "credits,images,alternative_titles,translations,external_ids,trailers,release_dates",
				"language":           language,
			}.AsUrlValues()
			resp, err := napping.Get(
				tmdbEndpoint+"movie/"+movieID,
				&urlValues,
				&movie,
				nil,
			)
			if err != nil {
				log.Error(err)
				xbmc.Notify("Elementum", fmt.Sprintf("Failed getting movie %s, check your logs.", movieID), config.AddonIcon())
			} else if resp.Status() == 429 {
				log.Warningf("Rate limit exceeded getting movie %s, cooling down...", movieID)
				rl.CoolDown(resp.HttpResponse().Header)
				return util.ErrExceeded
			} else if resp.Status() != 200 {
				message := fmt.Sprintf("Bad status getting movie %s: %d", movieID, resp.Status())
				log.Error(message)
				xbmc.Notify("Elementum", message, config.AddonIcon())
			}
			if movie != nil {
				cacheStore.Set(key, movie, cacheExpiration)
			}

			return nil
		})
	}
	if movie == nil {
		return nil
	}
	switch t := movie.RawPopularity.(type) {
	case string:
		popularity, _ := strconv.ParseFloat(t, 64)
		movie.Popularity = popularity
	case float64:
		movie.Popularity = t
	}
	return movie
}

// GetMovies ...
func GetMovies(tmdbIds []int, language string) Movies {
	var wg sync.WaitGroup
	movies := make(Movies, len(tmdbIds))
	wg.Add(len(tmdbIds))
	for i, tmdbID := range tmdbIds {
		go func(i int, tmdbId int) {
			defer wg.Done()
			movies[i] = GetMovie(tmdbId, language)
		}(i, tmdbID)
	}
	wg.Wait()
	return movies
}

// GetMovieGenres ...
func GetMovieGenres(language string) []*Genre {
	genres := GenreList{}

	cacheStore := cache.NewFileStore(path.Join(config.Get().ProfilePath, "cache"))
	key := fmt.Sprintf("com.tmdb.genres.movies.%s", language)
	if err := cacheStore.Get(key, &genres); err != nil {
		rl.Call(func() error {
			urlValues := napping.Params{
				"api_key":  apiKey,
				"language": language,
			}.AsUrlValues()
			resp, err := napping.Get(
				tmdbEndpoint+"genre/movie/list",
				&urlValues,
				&genres,
				nil,
			)
			if err != nil {
				log.Error(err)
				xbmc.Notify("Elementum", "Failed getting movie genres, check your logs.", config.AddonIcon())
			} else if resp.Status() == 429 {
				log.Warning("Rate limit exceeded getting genres, cooling down...")
				rl.CoolDown(resp.HttpResponse().Header)
				return util.ErrExceeded
			} else if resp.Status() != 200 {
				message := fmt.Sprintf("Bad status getting movie genres: %d", resp.Status())
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

// SearchMovies ...
func SearchMovies(query string, language string, page int) (Movies, int) {
	var results EntityList

	rl.Call(func() error {
		urlValues := napping.Params{
			"api_key": apiKey,
			"query":   query,
			"page":    strconv.Itoa(page),
		}.AsUrlValues()
		resp, err := napping.Get(
			tmdbEndpoint+"search/movie",
			&urlValues,
			&results,
			nil,
		)
		if err != nil {
			log.Error(err)
			xbmc.Notify("Elementum", "Failed searching movies, check your logs.", config.AddonIcon())
		} else if resp.Status() == 429 {
			log.Warningf("Rate limit exceeded searching movies with %s", query)
			rl.CoolDown(resp.HttpResponse().Header)
			return util.ErrExceeded
		} else if resp.Status() != 200 {
			message := fmt.Sprintf("Bad status searching movies: %d", resp.Status())
			log.Error(message)
			xbmc.Notify("Elementum", message, config.AddonIcon())
		}

		return nil
	})
	tmdbIds := make([]int, 0, len(results.Results))
	for _, movie := range results.Results {
		tmdbIds = append(tmdbIds, movie.ID)
	}
	return GetMovies(tmdbIds, language), results.TotalResults
}

// GetIMDBList ...
func GetIMDBList(listID string, language string, page int) (movies Movies, totalResults int) {
	var results *List
	totalResults = -1
	resultsPerPage := config.Get().ResultsPerPage
	limit := resultsPerPage * PagesAtOnce
	pageGroup := (page-1)*resultsPerPage/limit + 1

	cacheStore := cache.NewFileStore(path.Join(config.Get().ProfilePath, "cache"))
	key := fmt.Sprintf("com.imdb.list.%s.%d", listID, pageGroup)
	totalKey := fmt.Sprintf("com.imdb.list.%s.total", listID)
	if err := cacheStore.Get(key, &movies); err != nil {
		rl.Call(func() error {
			urlValues := napping.Params{
				"api_key": apiKey,
			}.AsUrlValues()
			resp, err := napping.Get(
				tmdbEndpoint+"list/"+listID,
				&urlValues,
				&results,
				nil,
			)
			if err != nil {
				log.Error(err)
				xbmc.Notify("Elementum", "Failed getting IMDb list, check your logs.", config.AddonIcon())
			} else if resp.Status() == 429 {
				log.Warning("Rate limit exceeded getting IMDb list, cooling down...")
				rl.CoolDown(resp.HttpResponse().Header)
				return util.ErrExceeded
			} else if resp.Status() != 200 {
				message := fmt.Sprintf("Bad status getting IMDb list: %d", resp.Status())
				log.Error(message + fmt.Sprintf(" (%s)", listID))
				xbmc.Notify("Elementum", message, config.AddonIcon())
			}

			return nil
		})
		tmdbIds := make([]int, 0)
		for i, movie := range results.Items {
			if i >= limit {
				break
			}
			tmdbIds = append(tmdbIds, movie.ID)
		}
		movies = GetMovies(tmdbIds, language)
		if movies != nil && len(movies) > 0 {
			cacheStore.Set(key, movies, cacheExpiration*4)
		}
		totalResults = results.ItemCount
		cacheStore.Set(totalKey, totalResults, cacheExpiration*4)
	} else {
		if err := cacheStore.Get(totalKey, &totalResults); err != nil {
			totalResults = -1
		}
	}
	return
}

func listMovies(endpoint string, cacheKey string, params napping.Params, page int) (Movies, int) {
	params["api_key"] = apiKey
	totalResults := -1
	genre := params["with_genres"]
	if params["with_genres"] == "" {
		genre = "all"
	}
	limit := ResultsPerPage * PagesAtOnce
	pageGroup := (page-1)*ResultsPerPage/limit + 1

	movies := make(Movies, limit)

	cacheStore := cache.NewFileStore(path.Join(config.Get().ProfilePath, "cache"))
	key := fmt.Sprintf("com.tmdb.topmovies.%s.%s.%d", cacheKey, genre, pageGroup)
	totalKey := fmt.Sprintf("com.tmdb.topmovies.%s.%s.total", cacheKey, genre)
	if err := cacheStore.Get(key, &movies); err != nil {
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
						xbmc.Notify("Elementum", "Failed while listing movies, check your logs.", config.AddonIcon())
					} else if resp.Status() == 429 {
						log.Warningf("Rate limit exceeded listing movies from %s, cooling down...", endpoint)
						rl.CoolDown(resp.HttpResponse().Header)
						return util.ErrExceeded
					} else if resp.Status() != 200 {
						message := fmt.Sprintf("Bad status while listing movies from %s: %d", endpoint, resp.Status())
						log.Error(message + fmt.Sprintf(" (%s)", endpoint))
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
					for m, movie := range results.Results {
						if movie == nil {
							wgItems.Done()
							continue
						}

						go func(i int, tmdbId int) {
							defer wgItems.Done()
							movies[i] = GetMovie(tmdbId, params["language"])
						}(p*ResultsPerPage+m, movie.ID)
					}
					wgItems.Wait()
				}
			}(p)
		}
		wg.Wait()
		cacheStore.Set(key, movies, recentExpiration)
	} else {
		if err := cacheStore.Get(totalKey, &totalResults); err != nil {
			totalResults = -1
		}
	}
	return movies, totalResults
}

// PopularMovies ...
func PopularMovies(genre string, language string, page int) (Movies, int) {
	var p napping.Params
	if genre == "" {
		p = napping.Params{
			"language":                 language,
			"sort_by":                  "popularity.desc",
			"primary_release_date.lte": time.Now().UTC().Format("2006-01-02"),
		}
	} else {
		p = napping.Params{
			"language":                 language,
			"sort_by":                  "popularity.desc",
			"primary_release_date.lte": time.Now().UTC().Format("2006-01-02"),
			"with_genres":              genre,
		}
	}
	return listMovies("discover/movie", "popular", p, page)
}

// RecentMovies ...
func RecentMovies(genre string, language string, page int) (Movies, int) {
	var p napping.Params
	if genre == "" {
		p = napping.Params{
			"language":                 language,
			"sort_by":                  "primary_release_date.desc",
			"vote_count.gte":           "10",
			"primary_release_date.lte": time.Now().UTC().Format("2006-01-02"),
		}
	} else {
		p = napping.Params{
			"language":                 language,
			"sort_by":                  "primary_release_date.desc",
			"vote_count.gte":           "10",
			"primary_release_date.lte": time.Now().UTC().Format("2006-01-02"),
			"with_genres":              genre,
		}
	}
	return listMovies("discover/movie", "recent", p, page)
}

// TopRatedMovies ...
func TopRatedMovies(genre string, language string, page int) (Movies, int) {
	return listMovies("movie/top_rated", "toprated", napping.Params{"language": language}, page)
}

// MostVotedMovies ...
func MostVotedMovies(genre string, language string, page int) (Movies, int) {
	var p napping.Params
	if genre == "" {
		p = napping.Params{
			"language":                 language,
			"sort_by":                  "vote_count.desc",
			"primary_release_date.lte": time.Now().UTC().Format("2006-01-02"),
		}
	} else {
		p = napping.Params{
			"language":                 language,
			"sort_by":                  "vote_count.desc",
			"primary_release_date.lte": time.Now().UTC().Format("2006-01-02"),
			"with_genres":              genre,
		}
	}
	return listMovies("discover/movie", "mostvoted", p, page)
}

// ToListItem ...
func (movie *Movie) ToListItem() *xbmc.ListItem {
	year, _ := strconv.Atoi(strings.Split(movie.ReleaseDate, "-")[0])

	title := movie.Title
	if config.Get().UseOriginalTitle && movie.OriginalTitle != "" {
		title = movie.OriginalTitle
	}

	item := &xbmc.ListItem{
		Label:  title,
		Label2: fmt.Sprintf("%f", movie.VoteAverage),
		Info: &xbmc.ListItemInfo{
			Year:          year,
			Count:         rand.Int(),
			Title:         title,
			OriginalTitle: movie.OriginalTitle,
			Plot:          movie.Overview,
			PlotOutline:   movie.Overview,
			TagLine:       movie.TagLine,
			Duration:      movie.Runtime * 60,
			Code:          movie.IMDBId,
			IMDBNumber:    movie.IMDBId,
			Date:          movie.ReleaseDate,
			Votes:         strconv.Itoa(movie.VoteCount),
			Rating:        movie.VoteAverage,
			DBTYPE:        "movie",
			Mediatype:     "movie",
		},
		Art: &xbmc.ListItemArt{
			FanArt: ImageURL(movie.BackdropPath, "w1280"),
			Poster: ImageURL(movie.PosterPath, "w500"),
		},
	}

	item.Thumbnail = item.Art.Poster
	item.Art.Thumbnail = item.Art.Poster
	genres := make([]string, 0, len(movie.Genres))
	for _, genre := range movie.Genres {
		genres = append(genres, genre.Name)
	}
	item.Info.Genre = strings.Join(genres, " / ")

	if movie.Trailers != nil {
		for _, trailer := range movie.Trailers.Youtube {
			item.Info.Trailer = trailer.Source
			break
		}
	}

	if item.Info.Trailer == "" && config.Get().Language != "en" {
		enMovie := GetMovie(movie.ID, "en")
		if enMovie.Trailers != nil {
			for _, trailer := range enMovie.Trailers.Youtube {
				item.Info.Trailer = trailer.Source
				break
			}
		}
	}

	for _, language := range movie.SpokenLanguages {
		item.StreamInfo = &xbmc.StreamInfo{
			Audio: &xbmc.StreamInfoEntry{
				Language: language.Iso639_1,
			},
		}
		break
	}

	for _, company := range movie.ProductionCompanies {
		item.Info.Studio = company.Name
		break
	}
	if movie.Credits != nil {
		item.Info.CastAndRole = make([][]string, 0)
		for _, cast := range movie.Credits.Cast {
			item.Info.CastAndRole = append(item.Info.CastAndRole, []string{cast.Name, cast.Character})
		}
		directors := make([]string, 0)
		writers := make([]string, 0)
		for _, crew := range movie.Credits.Crew {
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
