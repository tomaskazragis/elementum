package tmdb

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/elgatito/elementum/cache"
	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/playcount"
	"github.com/elgatito/elementum/util"
	"github.com/elgatito/elementum/xbmc"
	"github.com/jmcvetta/napping"
)

// GetEpisode ...
func GetEpisode(showID int, seasonNumber int, episodeNumber int, language string) *Episode {
	var episode *Episode
	cacheStore := cache.NewDBStore()
	key := fmt.Sprintf("com.tmdb.episode.%d.%d.%d.%s", showID, seasonNumber, episodeNumber, language)
	if err := cacheStore.Get(key, &episode); err != nil {
		rl.Call(func() error {
			urlValues := napping.Params{
				"api_key":            apiKey,
				"append_to_response": "credits,images,videos,external_ids",
				"language":           language,
			}.AsUrlValues()
			resp, err := napping.Get(
				fmt.Sprintf("%stv/%d/season/%d/episode/%d", tmdbEndpoint, showID, seasonNumber, episodeNumber),
				&urlValues,
				&episode,
				nil,
			)
			if err != nil {
				log.Error(err.Error())
				// xbmc.Notify("Elementum", fmt.Sprintf("Failed getting S%02dE%02d of %d, check your logs.", seasonNumber, episodeNumber, showID), config.AddonIcon())
			} else if resp.Status() == 429 {
				log.Warningf("Rate limit exceeded getting S%02dE%02d of %d, cooling down...", seasonNumber, episodeNumber, showID)
				rl.CoolDown(resp.HttpResponse().Header)
				return util.ErrExceeded
			} else if resp.Status() != 200 {
				message := fmt.Sprintf("Bad status getting S%02dE%02d of %d: %d", seasonNumber, episodeNumber, showID, resp.Status())
				log.Error(message)
				// xbmc.Notify("Elementum", message, config.AddonIcon())
				return util.ErrHTTP
			}

			return nil
		})

		if episode != nil {
			cacheStore.Set(key, episode, cacheExpiration)
		}
	}
	return episode
}

// ToListItems ...
func (episodes EpisodeList) ToListItems(show *Show, season *Season) []*xbmc.ListItem {
	items := make([]*xbmc.ListItem, 0, len(episodes))
	if len(episodes) == 0 {
		return items
	}

	fanarts := make([]string, 0)
	for _, backdrop := range show.Images.Backdrops {
		fanarts = append(fanarts, ImageURL(backdrop.FilePath, "w1280"))
	}

	now := time.Now().UTC()
	for _, episode := range episodes {
		if config.Get().ShowUnairedEpisodes == false {
			if episode.AirDate == "" {
				continue
			}
			firstAired, _ := time.Parse("2006-01-02", episode.AirDate)
			if firstAired.After(now) {
				continue
			}
		}

		item := episode.ToListItem(show)

		if item.Art.FanArt == "" && len(fanarts) > 0 {
			item.Art.FanArt = fanarts[rand.Intn(len(fanarts))]
		}

		item.Art.Poster = ImageURL(season.Poster, "w500")

		items = append(items, item)
	}
	return items
}

// ToListItem ...
func (episode *Episode) ToListItem(show *Show) *xbmc.ListItem {
	episodeLabel := fmt.Sprintf("%dx%02d %s", episode.SeasonNumber, episode.EpisodeNumber, episode.Name)

	runtime := 1800
	if len(show.EpisodeRunTime) > 0 {
		runtime = show.EpisodeRunTime[len(show.EpisodeRunTime)-1] * 60
	}

	item := &xbmc.ListItem{
		Label:  episodeLabel,
		Label2: fmt.Sprintf("%f", episode.VoteAverage),
		Info: &xbmc.ListItemInfo{
			Count:         rand.Int(),
			Title:         episodeLabel,
			OriginalTitle: episode.Name,
			Season:        episode.SeasonNumber,
			Episode:       episode.EpisodeNumber,
			TVShowTitle:   show.Name,
			Plot:          episode.Overview,
			PlotOutline:   episode.Overview,
			Rating:        episode.VoteAverage,
			Aired:         episode.AirDate,
			Duration:      runtime,
			Code:          show.ExternalIDs.IMDBId,
			IMDBNumber:    show.ExternalIDs.IMDBId,
			PlayCount:     playcount.GetWatchedEpisodeByTMDB(show.ID, episode.SeasonNumber, episode.EpisodeNumber).Int(),
			DBTYPE:        "episode",
			Mediatype:     "episode",
		},
		Art: &xbmc.ListItemArt{},
	}

	if episode.StillPath != "" {
		item.Art.FanArt = ImageURL(episode.StillPath, "w1280")
		item.Art.Thumbnail = ImageURL(episode.StillPath, "w500")
	}

	return item
}
