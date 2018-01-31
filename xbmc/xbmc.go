package xbmc

import "time"

// UpdateAddonRepos ...
func UpdateAddonRepos() (retVal string) {
	executeJSONRPCEx("UpdateAddonRepos", &retVal, nil)
	return
}

// ResetRPC ...
func ResetRPC() (retVal string) {
	executeJSONRPCEx("Reset", &retVal, nil)
	return
}

// Refresh ...
func Refresh() (retVal string) {
	executeJSONRPCEx("Refresh", &retVal, nil)
	return
}

// VideoLibraryScan ...
func VideoLibraryScan() (retVal string) {
	executeJSONRPC("VideoLibrary.Scan", &retVal, nil)
	return
}

// VideoLibraryClean ...
func VideoLibraryClean() (retVal string) {
	executeJSONRPC("VideoLibrary.Clean", &retVal, nil)
	return
}

// VideoLibraryGetMovies ...
func VideoLibraryGetMovies() (movies *VideoLibraryMovies, err error) {
	params := map[string]interface{}{"properties": []interface{}{
		"imdbnumber",
		"playcount",
		"file",
		"resume",
		"uniqueid",
		"premiered",
	}}
	err = executeJSONRPCO("VideoLibrary.GetMovies", &movies, params)
	if err != nil {
		log.Errorf("Error getting movies: %#v", err)
	}
	return
}

// PlayerGetActive ...
func PlayerGetActive() int {
	params := map[string]interface{}{}
	items := ActivePlayers{}
	executeJSONRPCO("Player.GetActivePlayers", &items, params)
	for _, v := range items {
		if v.Type == "video" {
			return v.ID
		}
	}

	return -1
}

// PlayerGetItem ...
func PlayerGetItem(playerid int) (item *PlayerItemInfo) {
	params := map[string]interface{}{
		"playerid": playerid,
	}
	executeJSONRPCO("Player.GetItem", &item, params)
	return
}

// VideoLibraryGetShows ...
func VideoLibraryGetShows() (shows *VideoLibraryShows, err error) {
	params := map[string]interface{}{
		"properties": []interface{}{
			"imdbnumber",
			"episode",
			"uniqueid",
			"premiered",
		},
	}
	err = executeJSONRPCO("VideoLibrary.GetTVShows", &shows, params)
	if err != nil {
		log.Errorf("Error getting tvshows: %#v", err)
	}
	return
}

// VideoLibraryGetSeasons ...
func VideoLibraryGetSeasons(tvshowID int) (seasons *VideoLibrarySeasons, err error) {
	params := map[string]interface{}{"tvshowid": tvshowID, "properties": []interface{}{
		"tvshowid",
		"season",
		"episode",
		"playcount",
	}}
	err = executeJSONRPCO("VideoLibrary.GetSeasons", &seasons, params)
	if err != nil {
		log.Errorf("Error getting seasons: %#v", err)
	}
	return
}

// VideoLibraryGetAllSeasons ...
func VideoLibraryGetAllSeasons() (seasons *VideoLibrarySeasons, err error) {
	params := map[string]interface{}{"properties": []interface{}{
		"tvshowid",
		"season",
		"episode",
		"playcount",
	}}
	err = executeJSONRPCO("VideoLibrary.GetSeasons", &seasons, params)
	if err != nil {
		log.Errorf("Error getting seasons: %#v", err)
	}
	return
}

// VideoLibraryGetEpisodes ...
func VideoLibraryGetEpisodes(tvshowID int) (episodes *VideoLibraryEpisodes, err error) {
	params := map[string]interface{}{"tvshowid": tvshowID, "properties": []interface{}{
		"tvshowid",
		"uniqueid",
		"season",
		"episode",
		"playcount",
		"file",
		"resume",
	}}
	err = executeJSONRPCO("VideoLibrary.GetEpisodes", &episodes, params)
	if err != nil {
		log.Errorf("Error getting episodes: %#v", err)
	}
	return
}

// VideoLibraryGetAllEpisodes ...
func VideoLibraryGetAllEpisodes() (episodes *VideoLibraryEpisodes, err error) {
	params := map[string]interface{}{"properties": []interface{}{
		"tvshowid",
		"uniqueid",
		"season",
		"episode",
		"playcount",
		"file",
		"resume",
	}}
	err = executeJSONRPCO("VideoLibrary.GetEpisodes", &episodes, params)
	if err != nil {
		log.Error(err)
	}
	return
}

// SetMovieWatched ...
func SetMovieWatched(movieID int, playcount int, position int, total int) (ret string) {
	params := map[string]interface{}{
		"movieid":   movieID,
		"playcount": playcount,
		"resume": map[string]interface{}{
			"position": position,
			"total":    total,
		},
		"lastplayed": time.Now().Format("2006-01-02 15:04:05"),
	}
	executeJSONRPCO("VideoLibrary.SetMovieDetails", &ret, params)
	return
}

// SetShowWatched ...
func SetShowWatched(showID int, playcount int) (ret string) {
	params := map[string]interface{}{
		"tvshowid":  showID,
		"playcount": playcount,
	}
	executeJSONRPCO("VideoLibrary.SetTVShowDetails", &ret, params)
	return
}

// SetEpisodeWatched ...
func SetEpisodeWatched(episodeID int, playcount int, position int, total int) (ret string) {
	params := map[string]interface{}{
		"episodeid": episodeID,
		"playcount": playcount,
		"resume": map[string]interface{}{
			"position": position,
			"total":    total,
		},
		"lastplayed": time.Now().Format("2006-01-02 15:04:05"),
	}
	executeJSONRPCO("VideoLibrary.SetEpisodeDetails", &ret, params)
	return
}

// SetFileWatched ...
func SetFileWatched(file string, position int, total int) (ret string) {
	params := map[string]interface{}{
		"file":      file,
		"media":     "video",
		"playcount": 0,
		"resume": map[string]interface{}{
			"position": position,
			"total":    total,
		},
		"lastplayed": time.Now().Format("2006-01-02 15:04:05"),
	}
	executeJSONRPCO("VideoLibrary.SetFileDetails", &ret, params)
	return
}

// TranslatePath ...
func TranslatePath(path string) (retVal string) {
	executeJSONRPCEx("TranslatePath", &retVal, Args{path})
	return
}

// UpdatePath ...
func UpdatePath(path string) (retVal string) {
	executeJSONRPCEx("Update", &retVal, Args{path})
	return
}

// PlayURL ...
func PlayURL(url string) {
	retVal := ""
	executeJSONRPCEx("Player_Open", &retVal, Args{url})
}

const (
	// Iso639_1 ...
	Iso639_1 = iota
	// Iso639_2 ...
	Iso639_2
	// EnglishName ...
	EnglishName
)

// ConvertLanguage ...
func ConvertLanguage(language string, format int) string {
	retVal := ""
	executeJSONRPCEx("ConvertLanguage", &retVal, Args{language, format})
	return retVal
}

// FilesGetSources ...
func FilesGetSources() *FileSources {
	params := map[string]interface{}{
		"media": "video",
	}
	items := &FileSources{}
	executeJSONRPCO("Files.GetSources", items, params)

	return items
}

// GetLanguage ...
func GetLanguage(format int) string {
	retVal := ""
	executeJSONRPCEx("GetLanguage", &retVal, Args{format})
	return retVal
}

// GetLanguageISO639_1 ...
func GetLanguageISO639_1() string {
	language := GetLanguage(Iso639_1)
	if language == "" {
		switch GetLanguage(EnglishName) {
		case "Chinese (Simple)":
			return "zh"
		case "Chinese (Traditional)":
			return "zh"
		case "English (Australia)":
			return "en"
		case "English (New Zealand)":
			return "en"
		case "English (US)":
			return "en"
		case "French (Canada)":
			return "fr"
		case "Hindi (Devanagiri)":
			return "hi"
		case "Mongolian (Mongolia)":
			return "mn"
		case "Persian (Iran)":
			return "fa"
		case "Portuguese (Brazil)":
			return "pt"
		case "Serbian (Cyrillic)":
			return "sr"
		case "Spanish (Argentina)":
			return "es"
		case "Spanish (Mexico)":
			return "es"
		case "Tamil (India)":
			return "ta"
		default:
			return "en"
		}
	}
	return language
}
