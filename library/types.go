package library

import (
	"sync"

	"github.com/elgatito/elementum/xbmc"
)

// Library represents library
type Library struct {
	mu lMutex

	// Stores all the unique IDs collected
	UIDs map[int]*UniqueIDs

	// Stores Library Movies
	Movies map[int]*Movie

	// Stored Library Shows
	Shows map[int]*Show

	WatchedTrakt map[uint64]bool
}

type lMutex struct {
	UIDs   sync.RWMutex
	Movies sync.RWMutex
	Shows  sync.RWMutex
	Trakt  sync.RWMutex
}

// UniqueIDs represents all IDs for a library item
type UniqueIDs struct {
	MediaType int    `json:"media"`
	Kodi      int    `json:"kodi"`
	TMDB      int    `json:"tmdb"`
	TVDB      int    `json:"tvdb"`
	IMDB      string `json:"imdb"`
	Trakt     int    `json:"trakt"`
	Watched   bool   `json:"watched"`
}

// Movie represents Movie content type
type Movie struct {
	ID      int
	Title   string
	Watched bool
	File    string
	UIDs    *UniqueIDs
	Resume  *Resume
	Xbmc    *xbmc.VideoLibraryMovieItem
}

// Show represents Show content type
type Show struct {
	ID       int
	Title    string
	Watched  bool
	Seasons  map[int]*Season
	Episodes map[int]*Episode
	UIDs     *UniqueIDs
	Xbmc     *xbmc.VideoLibraryShowItem
}

// Season represents Season content type
type Season struct {
	ID       int
	Title    string
	Season   int
	Episodes int
	Watched  bool
	UIDs     *UniqueIDs
	Xbmc     *xbmc.VideoLibrarySeasonItem
}

// Episode represents Episode content type
type Episode struct {
	ID      int
	Title   string
	Season  int
	Episode int
	Watched bool
	UIDs    *UniqueIDs
	Resume  *Resume
	Xbmc    *xbmc.VideoLibraryEpisodeItem
}

// Resume shows watched progress information
type Resume struct {
	Position float64 `json:"position"`
	Total    float64 `json:"total"`
}

// DBItem ...
type DBItem struct {
	ID       string `json:"id"`
	Type     int    `json:"type"`
	TVShowID int    `json:"showid"`
}

type removedEpisode struct {
	ID        int
	ShowID    int
	ScraperID string
	ShowName  string
	Season    int
	Episode   int
}
