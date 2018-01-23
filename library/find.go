package library

import (
	"errors"

	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/tmdb"
	"github.com/elgatito/elementum/xbmc"
)

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
	if movie.ID == 0 || len(l.Movies) == 0 {
		return nil
	}

	l.mu.Movies.RLock()
	defer l.mu.Movies.RUnlock()

	for _, existingMovie := range l.Movies {
		if existingMovie.UIDs.TMDB == movie.ID {
			return existingMovie.Xbmc
		}
	}

	return nil
}

// FindEpisodeInLibrary ...
func FindEpisodeInLibrary(show *tmdb.Show, episode *tmdb.Episode) *xbmc.VideoLibraryEpisodeItem {
	if episode == nil || show == nil {
		return nil
	}

	l.mu.Shows.RLock()
	defer l.mu.Shows.RUnlock()

	for _, existingShow := range l.Shows {
		if existingShow.UIDs.TMDB != show.ID {
			continue
		}

		for _, existingEpisode := range existingShow.Episodes {
			if existingEpisode.Season == episode.SeasonNumber && existingEpisode.Episode == episode.EpisodeNumber {
				return existingEpisode.Xbmc
			}
		}
	}

	return nil
}

// GetLibraryMovie finds Movie from library
func GetLibraryMovie(kodiID int) *Movie {
	l.mu.Movies.Lock()
	defer l.mu.Movies.Unlock()

	for _, m := range l.Movies {
		if m.UIDs.Kodi == kodiID {
			return m
		}
	}

	return nil
}

// GetLibraryShow finds Show from library
func GetLibraryShow(kodiID int) *Show {
	l.mu.Shows.Lock()
	defer l.mu.Shows.Unlock()

	// query := strconv.Itoa(kodiID)
	for _, s := range l.Shows {
		if s.UIDs.Kodi == kodiID {
			return s
		}
	}

	return nil
}

// GetLibrarySeason finds Show/Season from library
func GetLibrarySeason(kodiID int) (*Show, *Season) {
	l.mu.Shows.Lock()
	defer l.mu.Shows.Unlock()

	for _, s := range l.Shows {
		for _, se := range s.Seasons {
			if se.UIDs.Kodi == kodiID {
				return s, se
			}
		}
	}

	return nil, nil
}

// GetLibraryEpisode finds Show/Episode from library
func GetLibraryEpisode(kodiID int) (*Show, *Episode) {
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

// GetMovieByTMDB ...
func GetMovieByTMDB(id int) (*Movie, error) {
	l.mu.Movies.Lock()
	defer l.mu.Movies.Unlock()

	for _, m := range l.Movies {
		if m != nil && m.UIDs.TMDB == id {
			return m, nil
		}
	}

	return nil, errors.New("Not found")
}

// GetMovieByIMDB ...
func GetMovieByIMDB(id string) (*Movie, error) {
	l.mu.Movies.Lock()
	defer l.mu.Movies.Unlock()

	for _, m := range l.Movies {
		if m != nil && m.UIDs.IMDB == id {
			return m, nil
		}
	}

	return nil, errors.New("Not found")
}

// GetShowByTMDB ...
func GetShowByTMDB(id int) (*Show, error) {
	l.mu.Shows.Lock()
	defer l.mu.Shows.Unlock()

	for _, s := range l.Shows {
		if s != nil && s.UIDs.TMDB == id {
			return s, nil
		}
	}

	return nil, errors.New("Not found")
}

// GetShowByIMDB ...
func GetShowByIMDB(id string) (*Show, error) {
	l.mu.Shows.Lock()
	defer l.mu.Shows.Unlock()

	for _, s := range l.Shows {
		if s != nil && s.UIDs.IMDB == id {
			return s, nil
		}
	}

	return nil, errors.New("Not found")
}

// GetEpisode ...
func (s *Show) GetEpisode(season, episode int) *Episode {
	for _, e := range s.Episodes {
		if e.Season == season && e.Episode == episode {
			return e
		}
	}

	return nil
}
