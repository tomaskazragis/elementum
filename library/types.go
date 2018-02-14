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
	Playcount int    `json:"playcount"`
}

// Movie represents Movie content type
type Movie struct {
	ID       int
	Title    string
	File     string
	Year     int
	UIDs     *UniqueIDs
	XbmcUIDs *xbmc.UniqueIDs
	Resume   *Resume
}

// Show represents Show content type
type Show struct {
	ID       int
	Title    string
	Year     int
	Seasons  map[int]*Season
	Episodes map[int]*Episode
	UIDs     *UniqueIDs
	XbmcUIDs *xbmc.UniqueIDs
}

// Season represents Season content type
type Season struct {
	ID       int
	Title    string
	Season   int
	Episodes int
	UIDs     *UniqueIDs
	XbmcUIDs *xbmc.UniqueIDs
}

// Episode represents Episode content type
type Episode struct {
	ID       int
	Title    string
	Season   int
	Episode  int
	File     string
	UIDs     *UniqueIDs
	XbmcUIDs *xbmc.UniqueIDs
	Resume   *Resume
}

// Resume shows watched progress information
type Resume struct {
	Position float64 `json:"position"`
	Total    float64 `json:"total"`
}

// DBItem ...
type DBItem struct {
	ID       int `json:"id"`
	State    int `json:"state"`
	Type     int `json:"type"`
	TVShowID int `json:"showid"`
}

type removedEpisode struct {
	ID       int
	ShowID   int
	ShowName string
	Season   int
	Episode  int
}
