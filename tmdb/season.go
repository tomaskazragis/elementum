package tmdb

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/elgatito/elementum/cache"
	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/fanart"
	"github.com/elgatito/elementum/playcount"
	"github.com/elgatito/elementum/util"
	"github.com/elgatito/elementum/xbmc"
	"github.com/jmcvetta/napping"
)

// GetSeason ...
func GetSeason(showID int, seasonNumber int, language string) *Season {
	var season *Season
	cacheStore := cache.NewDBStore()
	key := fmt.Sprintf("com.tmdb.season.%d.%d.%s", showID, seasonNumber, language)
	if err := cacheStore.Get(key, &season); err != nil {
		rl.Call(func() error {
			urlValues := napping.Params{
				"api_key":            apiKey,
				"append_to_response": "credits,images,videos,external_ids,alternative_titles,translations,trailers",
				"language":           language,
			}.AsUrlValues()
			resp, err := napping.Get(
				fmt.Sprintf("%stv/%d/season/%d", tmdbEndpoint, showID, seasonNumber),
				&urlValues,
				&season,
				nil,
			)
			if err != nil {
				log.Error(err.Error())
				// xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
			} else if resp.Status() == 429 {
				log.Warningf("Rate limit exceeded getting season %d of show %d, cooling down...", seasonNumber, showID)
				rl.CoolDown(resp.HttpResponse().Header)
				return util.ErrExceeded
			} else if resp.Status() == 404 {
				cacheStore.Set(key, season, cacheHalfExpiration)
				message := fmt.Sprintf("Bad status getting season %d of show %d: %d", seasonNumber, showID, resp.Status())
				log.Error(message)
				return util.ErrHTTP
			} else if resp.Status() != 200 {
				message := fmt.Sprintf("Bad status getting season %d of show %d: %d", seasonNumber, showID, resp.Status())
				log.Error(message)
				// xbmc.Notify("Elementum", message, config.AddonIcon())
				return util.ErrHTTP
			}

			return nil
		})

		if season == nil {
			return nil
		}

		season.EpisodeCount = len(season.Episodes)

		// Fix for shows that have translations but return empty strings
		// for episode names and overviews.
		// We detect if episodes have their name filled, and if not re-query
		// with no language set.
		// See https://github.com/scakemyer/plugin.video.quasar/issues/249
		if season.EpisodeCount > 0 {
			for index := 0; index < season.EpisodeCount && index < len(season.Episodes); index++ {
				if season.Episodes[index] != nil && season.Episodes[index].Name == "" {
					season.Episodes[index] = GetEpisode(showID, seasonNumber, index+1, "")
				}
			}
		}

		if season != nil {
			updateFrequency := config.Get().UpdateFrequency * 60
			traktFrequency := config.Get().TraktSyncFrequency * 60
			if updateFrequency == 0 && traktFrequency == 0 {
				updateFrequency = 1440
			} else if updateFrequency > traktFrequency && traktFrequency != 0 {
				updateFrequency = traktFrequency - 1
			} else {
				updateFrequency = updateFrequency - 1
			}
			cacheStore.Set(key, season, time.Duration(updateFrequency)*time.Minute)
		}
	}
	return season
}

// ToListItems ...
func (seasons SeasonList) ToListItems(show *Show) []*xbmc.ListItem {
	items := make([]*xbmc.ListItem, 0, len(seasons))

	fanarts := make([]string, 0)
	for _, backdrop := range show.Images.Backdrops {
		fanarts = append(fanarts, ImageURL(backdrop.FilePath, "w1280"))
	}

	now := util.UTCBod()
	for _, season := range seasons {
		if season.EpisodeCount == 0 {
			continue
		}
		if config.Get().ShowUnairedSeasons == false {
			firstAired, _ := time.Parse("2006-01-02", season.AirDate)
			if firstAired.After(now) || firstAired.Equal(now) {
				continue
			}
		}

		item := season.ToListItem(show)

		if len(fanarts) > 0 {
			item.Art.FanArt = fanarts[rand.Intn(len(fanarts))]
		}

		items = append(items, item)
	}
	return items
}

func (seasons SeasonList) Len() int           { return len(seasons) }
func (seasons SeasonList) Swap(i, j int)      { seasons[i], seasons[j] = seasons[j], seasons[i] }
func (seasons SeasonList) Less(i, j int) bool { return seasons[i].Season < seasons[j].Season }

// ToListItem ...
func (season *Season) ToListItem(show *Show) *xbmc.ListItem {
	name := fmt.Sprintf("Season %d", season.Season)
	if season.Name != "" {
		name = season.Name
	}
	if season.Season == 0 {
		name = "Specials"
	}

	item := &xbmc.ListItem{
		Label: name,
		Info: &xbmc.ListItemInfo{
			Count:         rand.Int(),
			Title:         name,
			OriginalTitle: name,
			Season:        season.Season,
			TVShowTitle:   show.OriginalName,
			Plot:          show.Overview,
			PlotOutline:   show.Overview,
			DBTYPE:        "season",
			Mediatype:     "season",
			Code:          show.ExternalIDs.IMDBId,
			IMDBNumber:    show.ExternalIDs.IMDBId,
			PlayCount:     playcount.GetWatchedSeasonByTMDB(show.ID, season.Season).Int(),
		},
		Art: &xbmc.ListItemArt{},
	}

	if season.Poster != "" {
		item.Art.Poster = ImageURL(season.Poster, "w500")
		item.Art.Thumbnail = ImageURL(season.Poster, "w500")
	}

	fanarts := make([]string, 0)
	for _, backdrop := range show.Images.Backdrops {
		fanarts = append(fanarts, ImageURL(backdrop.FilePath, "w1280"))
	}
	if len(fanarts) > 0 {
		item.Art.FanArt = fanarts[rand.Intn(len(fanarts))]
	}

	if fa := fanart.GetShow(util.StrInterfaceToInt(show.ExternalIDs.TVDBID)); fa != nil {
		item.Art = fa.ToSeasonListItemArt(season.Season, item.Art)
		item.Thumbnail = item.Art.Thumbnail
	}

	if len(show.Genres) > 0 {
		item.Info.Genre = show.Genres[0].Name
	}

	return item
}
