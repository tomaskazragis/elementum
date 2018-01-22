package trakt

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/elgatito/elementum/cache"
	"github.com/elgatito/elementum/cloudhole"
	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/util"
	"github.com/elgatito/elementum/xbmc"
	"github.com/jmcvetta/napping"
	"github.com/op/go-logging"
)

//go:generate msgp -o msgp.go -io=false -tests=false

const (
	// APIURL ...
	APIURL = "https://api.trakt.tv"
	// ClientID ...
	ClientID = "2f911cee953f0af7833191d2b929e9a842bf8752e6b1afb458c8ff9ffc1d2c85"
	// ClientSecret ...
	ClientSecret = "b290a36c1144c4baa937dcc9023b3cd44398cca46975928a3d833f7593f00980"
	// APIVersion ...
	APIVersion = "2"
)

var log = logging.MustGetLogger("trakt")

var (
	// PagesAtOnce ...
	PagesAtOnce             = 5
	clearance, _            = cloudhole.GetClearance()
	retriesLeft             = 3
	burstRate               = 50
	burstTime               = 10 * time.Second
	simultaneousConnections = 25
	cacheExpiration         = 6 * 24 * time.Hour
	recentExpiration        = 15 * time.Minute
	userlistExpiration      = 1 * time.Minute
	watchedExpiration       = 30 * time.Minute
)

var rl = util.NewRateLimiter(burstRate, burstTime, simultaneousConnections)

// Object ...
type Object struct {
	Title string `json:"title"`
	Year  int    `json:"year"`
	IDs   *IDs   `json:"ids"`
}

// MovieSearchResults ...
type MovieSearchResults []struct {
	Type  string      `json:"type"`
	Score interface{} `json:"score"`
	Movie *Movie
}

// ShowSearchResults ...
type ShowSearchResults []struct {
	Type  string      `json:"type"`
	Score interface{} `json:"score"`
	Show  *Show
}

// EpisodeSearchResults ...
type EpisodeSearchResults []struct {
	Type    string      `json:"type"`
	Score   interface{} `json:"score"`
	Episode *Episode
	Show    *Show
}

// Movie ...
type Movie struct {
	Object

	Released      string   `json:"released"`
	URL           string   `json:"homepage"`
	Trailer       string   `json:"trailer"`
	Runtime       int      `json:"runtime"`
	TagLine       string   `json:"tagline"`
	Overview      string   `json:"overview"`
	Certification string   `json:"certification"`
	Rating        float32  `json:"rating"`
	Votes         int      `json:"votes"`
	Genres        []string `json:"genres"`
	Language      string   `json:"language"`
	Translations  []string `json:"available_translations"`

	Images *Images `json:"images"`
}

// Show ...
type Show struct {
	Object

	FirstAired    string   `json:"first_aired"`
	URL           string   `json:"homepage"`
	Trailer       string   `json:"trailer"`
	Runtime       int      `json:"runtime"`
	Overview      string   `json:"overview"`
	Certification string   `json:"certification"`
	Status        string   `json:"status"`
	Network       string   `json:"network"`
	AiredEpisodes int      `json:"aired_episodes"`
	Airs          *Airs    `json:"airs"`
	Rating        float32  `json:"rating"`
	Votes         int      `json:"votes"`
	Genres        []string `json:"genres"`
	Country       string   `json:"country"`
	Language      string   `json:"language"`
	Translations  []string `json:"available_translations"`

	Images *Images `json:"images"`
}

// Season ...
type Season struct {
	// Show          *Show   `json:"-"`
	Number        int     `json:"number"`
	Overview      string  `json:"overview"`
	EpisodeCount  int     `json:"episode_count"`
	AiredEpisodes int     `json:"aired_episodes"`
	Rating        float32 `json:"rating"`
	Votes         int     `json:"votes"`

	Images *Images `json:"images"`
	IDs    *IDs    `json:"ids"`
}

// Episode ...
type Episode struct {
	// Show          *Show       `json:"-"`
	// Season        *ShowSeason `json:"-"`
	Number       int      `json:"number"`
	Season       int      `json:"season"`
	Title        string   `json:"title"`
	Overview     string   `json:"overview"`
	Absolute     int      `json:"number_abs"`
	FirstAired   string   `json:"first_aired"`
	Translations []string `json:"available_translations"`

	Rating float32 `json:"rating"`
	Votes  int     `json:"votes"`

	Images *Images `json:"images"`
	IDs    *IDs    `json:"ids"`
}

// Airs ...
type Airs struct {
	Day      string `json:"day"`
	Time     string `json:"time"`
	Timezone string `json:"timezone"`
}

// Movies ...
type Movies struct {
	Watchers int    `json:"watchers"`
	Movie    *Movie `json:"movie"`
}

// Shows ...
type Shows struct {
	Watchers int   `json:"watchers"`
	Show     *Show `json:"show"`
}

// Watchlist ...
type Watchlist struct {
	Movies   []*Movie   `json:"movies"`
	Shows    []*Show    `json:"shows"`
	Episodes []*Episode `json:"episodes"`
}

// WatchlistMovie ...
type WatchlistMovie struct {
	ListedAt string `json:"listed_at"`
	Type     string `json:"type"`
	Movie    *Movie `json:"movie"`
}

// WatchlistShow ...
type WatchlistShow struct {
	ListedAt string `json:"listed_at"`
	Type     string `json:"type"`
	Show     *Show  `json:"show"`
}

// WatchlistSeason ...
type WatchlistSeason struct {
	ListedAt string  `json:"listed_at"`
	Type     string  `json:"type"`
	Season   *Object `json:"season"`
	Show     *Object `json:"show"`
}

// WatchlistEpisode ...
type WatchlistEpisode struct {
	ListedAt string   `json:"listed_at"`
	Type     string   `json:"type"`
	Episode  *Episode `json:"episode"`
	Show     *Object  `json:"show"`
}

// CollectionMovie ...
type CollectionMovie struct {
	CollectedAt string `json:"collected_at"`
	Movie       *Movie `json:"movie"`
}

// CollectionShow ...
type CollectionShow struct {
	CollectedAt string             `json:"last_collected_at"`
	Show        *Show              `json:"show"`
	Seasons     []*CollectedSeason `json:"seasons"`
}

// CollectedSeason ...
type CollectedSeason struct {
	Number   int                 `json:"number"`
	Episodes []*CollectedEpisode `json:"episodes"`
}

// CollectedEpisode ...
type CollectedEpisode struct {
	CollectedAt string `json:"collected_at"`
	Number      int    `json:"number"`
}

// Images ...
type Images struct {
	Poster     *Sizes `json:"poster"`
	FanArt     *Sizes `json:"fanart"`
	ScreenShot *Sizes `json:"screenshot"`
	HeadShot   *Sizes `json:"headshot"`
	Logo       *Sizes `json:"logo"`
	ClearArt   *Sizes `json:"clearart"`
	Banner     *Sizes `json:"banner"`
	Thumbnail  *Sizes `json:"thumb"`
	Avatar     *Sizes `json:"avatar"`
}

// Sizes ...
type Sizes struct {
	Full      string `json:"full"`
	Medium    string `json:"medium"`
	Thumbnail string `json:"thumb"`
}

// IDs ...
type IDs struct {
	Trakt  int    `json:"trakt"`
	IMDB   string `json:"imdb"`
	TMDB   int    `json:"tmdb"`
	TVDB   int    `json:"tvdb"`
	TVRage int    `json:"tvrage"`
	Slug   string `json:"slug"`
}

// Code ...
type Code struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURL string `json:"verification_url"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// Token ...
type Token struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

// TokenRefresh ...
type TokenRefresh struct {
	RefreshToken string `json:"refresh_token"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RedirectURI  string `json:"redirect_uri"`
	GrantType    string `json:"grant_type"`
}

// List ...
type List struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	Privacy        string `json:"privacy"`
	DisplayNumbers bool   `json:"display_numbers"`
	AllowComments  bool   `json:"allow_comments"`
	SortBy         string `json:"sort_by"`
	SortHow        string `json:"sort_how"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
	ItemCount      int    `json:"item_count"`
	CommentCount   int    `json:"comment_count"`
	Likes          int    `json:"likes"`
	IDs            *IDs
}

// ListItem ...
type ListItem struct {
	Rank     int    `json:"rank"`
	ListedAt string `json:"listed_at"`
	Type     string `json:"type"`
	Movie    *Movie `json:"movie"`
	Show     *Show  `json:"show"`
	// Season    *Season  `json:"season"`
	// Episode   *Episode `json:"episode"`
}

// CalendarShow ...
type CalendarShow struct {
	FirstAired string   `json:"first_aired"`
	Episode    *Episode `json:"episode"`
	Show       *Show    `json:"show"`
}

// CalendarMovie ...
type CalendarMovie struct {
	Released string `json:"released"`
	Movie    *Movie `json:"movie"`
}

// UserSettings ...
type UserSettings struct {
	User struct {
		Username string `json:"username"`
		Name     string `json:"name"`
	} `json:"user"`
	Account struct{} `json:"account"`
}

// WatchedItem represents possible watched add/delete item
type WatchedItem struct {
	MediaType string
	KodiID    int
	Movie     int
	Show      int
	Season    int
	Episode   int
	Watched   bool
}

// WatchedMovie ...
type WatchedMovie struct {
	Plays         int       `json:"plays"`
	LastWatchedAt time.Time `json:"last_watched_at"`
	Movie         struct {
		Title string `json:"title"`
		Year  int    `json:"year"`
		Ids   struct {
			Trakt int    `json:"trakt"`
			Slug  string `json:"slug"`
			Imdb  string `json:"imdb"`
			Tmdb  int    `json:"tmdb"`
		} `json:"ids"`
	} `json:"movie"`
}

// WatchedShow ...
type WatchedShow struct {
	Plays         int `json:"plays"`
	Watched       bool
	LastWatchedAt time.Time `json:"last_watched_at"`
	Show          struct {
		Title string `json:"title"`
		Year  int    `json:"year"`
		Ids   struct {
			Trakt  int    `json:"trakt"`
			Slug   string `json:"slug"`
			Tvdb   int    `json:"tvdb"`
			Imdb   string `json:"imdb"`
			Tmdb   int    `json:"tmdb"`
			Tvrage int    `json:"tvrage"`
		} `json:"ids"`
	} `json:"show"`
	Seasons []struct {
		Plays    int `json:"plays"`
		Number   int `json:"number"`
		Episodes []struct {
			Number        int       `json:"number"`
			Plays         int       `json:"plays"`
			LastWatchedAt time.Time `json:"last_watched_at"`
		} `json:"episodes"`
	} `json:"seasons"`
}

func totalFromHeaders(headers http.Header) (total int, err error) {
	if len(headers) > 0 {
		if itemCount, exists := headers["X-Pagination-Item-Count"]; exists {
			if itemCount != nil {
				total, err = strconv.Atoi(itemCount[0])
				return
			}
			return -1, errors.New("X-Pagination-Item-Count was empty")
		}
		return -1, errors.New("No X-Pagination-Item-Count header found")
	}
	return -1, errors.New("No valid headers in request")
}

func newClearance() (err error) {
	retriesLeft--
	log.Warningf("CloudFlared! User-Agent: %s - Cookies: %s", clearance.UserAgent, clearance.Cookies)

	if config.Get().UseCloudHole == false {
		retriesLeft = 0
		return errors.New("CloudFlared! Enable CloudHole in the add-on's settings")
	}

	clearance, err = cloudhole.GetClearance()
	if err == nil {
		log.Noticef("New clearance: %s - %s", clearance.UserAgent, clearance.Cookies)
	} else {
		retriesLeft = 0
	}
	return err
}

// Get ...
func Get(endPoint string, params url.Values) (resp *napping.Response, err error) {
	header := http.Header{
		"Content-type":      []string{"application/json"},
		"trakt-api-key":     []string{ClientID},
		"trakt-api-version": []string{APIVersion},
		"User-Agent":        []string{clearance.UserAgent},
		"Cookie":            []string{clearance.Cookies},
	}

	req := napping.Request{
		Url:    fmt.Sprintf("%s/%s", APIURL, endPoint),
		Method: "GET",
		Params: &params,
		Header: &header,
	}

	rl.Call(func() error {
		resp, err = napping.Send(&req)
		if err != nil {
			return err
		} else if resp.Status() == 429 {
			log.Warningf("Rate limit exceeded getting %s, cooling down...", endPoint)
			rl.CoolDown(resp.HttpResponse().Header)
			return util.ErrExceeded
		} else if resp.Status() == 403 && retriesLeft > 0 {
			err = newClearance()
			if err == nil {
				resp, err = Get(endPoint, params)
			}
		}

		return nil
	})
	return
}

// GetWithAuth ...
func GetWithAuth(endPoint string, params url.Values) (resp *napping.Response, err error) {
	header := http.Header{
		"Content-type":      []string{"application/json"},
		"Authorization":     []string{fmt.Sprintf("Bearer %s", config.Get().TraktToken)},
		"trakt-api-key":     []string{ClientID},
		"trakt-api-version": []string{APIVersion},
		"User-Agent":        []string{clearance.UserAgent},
		"Cookie":            []string{clearance.Cookies},
	}

	req := napping.Request{
		Url:    fmt.Sprintf("%s/%s", APIURL, endPoint),
		Method: "GET",
		Params: &params,
		Header: &header,
	}

	rl.Call(func() error {
		resp, err = napping.Send(&req)

		if err != nil {
			return err
		} else if resp.Status() == 429 {
			log.Warningf("Rate limit exceeded getting %s, cooling down...", endPoint)
			rl.CoolDown(resp.HttpResponse().Header)
			return util.ErrExceeded
		} else if resp.Status() == 403 && retriesLeft > 0 {
			err = newClearance()
			if err == nil {
				resp, err = GetWithAuth(endPoint, params)
			}
		}

		return nil
	})
	return
}

// Post ...
func Post(endPoint string, payload *bytes.Buffer) (resp *napping.Response, err error) {
	header := http.Header{
		"Content-type":      []string{"application/json"},
		"Authorization":     []string{fmt.Sprintf("Bearer %s", config.Get().TraktToken)},
		"trakt-api-key":     []string{ClientID},
		"trakt-api-version": []string{APIVersion},
		"User-Agent":        []string{clearance.UserAgent},
		"Cookie":            []string{clearance.Cookies},
	}

	req := napping.Request{
		Url:        fmt.Sprintf("%s/%s", APIURL, endPoint),
		Method:     "POST",
		RawPayload: true,
		Payload:    payload,
		Header:     &header,
	}

	rl.Call(func() error {
		resp, err = napping.Send(&req)
		if err != nil {
			return err
		} else if resp.Status() == 429 {
			log.Warningf("Rate limit exceeded getting %s, cooling down...", endPoint)
			rl.CoolDown(resp.HttpResponse().Header)
			return util.ErrExceeded
		} else if resp.Status() == 403 && retriesLeft > 0 {
			err = newClearance()
			if err == nil {
				resp, err = Post(endPoint, payload)
			}
		}

		return nil
	})
	return
}

// GetCode ...
func GetCode() (code *Code, err error) {
	endPoint := "oauth/device/code"
	header := http.Header{
		"Content-type": []string{"application/json"},
		"User-Agent":   []string{clearance.UserAgent},
		"Cookie":       []string{clearance.Cookies},
	}
	params := napping.Params{
		"client_id": ClientID,
	}.AsUrlValues()

	req := napping.Request{
		Url:    fmt.Sprintf("%s/%s", APIURL, endPoint),
		Method: "POST",
		Params: &params,
		Header: &header,
	}

	var resp *napping.Response
	rl.Call(func() error {
		resp, err = napping.Send(&req)
		if err != nil {
			err = resp.Unmarshal(&code)
			return err
		} else if resp.Status() == 429 {
			log.Warningf("Rate limit exceeded getting Trakt code %s, cooling down...", code)
			rl.CoolDown(resp.HttpResponse().Header)
			return util.ErrExceeded
		} else if resp.Status() == 403 && retriesLeft > 0 {
			err = newClearance()
			if err == nil {
				code, err = GetCode()
			}
		} else {
			resp.Unmarshal(&code)
		}

		return nil
	})
	if err == nil && resp.Status() != 200 {
		err = fmt.Errorf("Unable to get Trakt code: %d", resp.Status())
	}
	return
}

// GetToken ...
func GetToken(code string) (resp *napping.Response, err error) {
	endPoint := "oauth/device/token"
	header := http.Header{
		"Content-type": []string{"application/json"},
		"User-Agent":   []string{clearance.UserAgent},
		"Cookie":       []string{clearance.Cookies},
	}
	params := napping.Params{
		"code":          code,
		"client_id":     ClientID,
		"client_secret": ClientSecret,
	}.AsUrlValues()

	req := napping.Request{
		Url:    fmt.Sprintf("%s/%s", APIURL, endPoint),
		Method: "POST",
		Params: &params,
		Header: &header,
	}

	rl.Call(func() error {
		resp, err = napping.Send(&req)
		if err != nil {
			return err
		} else if resp.Status() == 429 {
			log.Warningf("Rate limit exceeded getting Trakt token with code %s, cooling down...", code)
			rl.CoolDown(resp.HttpResponse().Header)
			return util.ErrExceeded
		} else if resp.Status() == 403 && retriesLeft > 0 {
			err = newClearance()
			if err == nil {
				resp, err = GetToken(code)
			}
		}

		return nil
	})
	return
}

// PollToken ...
func PollToken(code *Code) (token *Token, err error) {
	startInterval := code.Interval
	interval := time.NewTicker(time.Duration(startInterval) * time.Second)
	defer interval.Stop()
	expired := time.NewTicker(time.Duration(code.ExpiresIn) * time.Second)
	defer expired.Stop()

	for {
		select {
		case <-interval.C:
			resp, errGet := GetToken(code.DeviceCode)
			if errGet != nil {
				return nil, errGet
			}
			if resp.Status() == 200 {
				resp.Unmarshal(&token)
				return token, err
			} else if resp.Status() == 400 {
				break
			} else if resp.Status() == 404 {
				err = errors.New("Invalid device code")
				return nil, err
			} else if resp.Status() == 409 {
				err = errors.New("Code already used")
				return nil, err
			} else if resp.Status() == 410 {
				err = errors.New("Code expired")
				return nil, err
			} else if resp.Status() == 418 {
				err = errors.New("Code denied")
				return nil, err
			} else if resp.Status() == 429 {
				// err = errors.New("Polling too quickly.")
				interval.Stop()
				interval = time.NewTicker(time.Duration(startInterval+5) * time.Second)
				break
			}

		case <-expired.C:
			err = errors.New("Code expired, please try again")
			return nil, err
		}
	}
}

// RefreshToken ...
func RefreshToken() (resp *napping.Response, err error) {
	endPoint := "oauth/token"
	header := http.Header{
		"Content-type": []string{"application/json"},
		"User-Agent":   []string{clearance.UserAgent},
		"Cookie":       []string{clearance.Cookies},
	}
	params := napping.Params{
		"refresh_token": config.Get().TraktRefreshToken,
		"client_id":     ClientID,
		"client_secret": ClientSecret,
		"redirect_uri":  "urn:ietf:wg:oauth:2.0:oob",
		"grant_type":    "refresh_token",
	}.AsUrlValues()

	req := napping.Request{
		Url:    fmt.Sprintf("%s/%s", APIURL, endPoint),
		Method: "POST",
		Params: &params,
		Header: &header,
	}

	resp, err = napping.Send(&req)
	if err != nil {
		return
	} else if resp.Status() == 403 && retriesLeft > 0 {
		err = newClearance()
		if err == nil {
			resp, err = RefreshToken()
		}
	}
	return
}

// TokenRefreshHandler ...
func TokenRefreshHandler() {
	if config.Get().TraktToken == "" {
		return
	}

	var token *Token
	ticker := time.NewTicker(12 * time.Hour)
	for {
		select {
		case <-ticker.C:
			if time.Now().Unix() > int64(config.Get().TraktTokenExpiry)-int64(259200) {
				resp, err := RefreshToken()
				if err != nil {
					xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
					log.Error(err)
					return
				}

				if resp.Status() == 200 {
					if errUnm := resp.Unmarshal(&token); errUnm != nil {
						xbmc.Notify("Elementum", errUnm.Error(), config.AddonIcon())
						log.Error(errUnm)
					} else {
						expiry := time.Now().Unix() + int64(token.ExpiresIn)
						xbmc.SetSetting("trakt_token_expiry", strconv.Itoa(int(expiry)))
						xbmc.SetSetting("trakt_token", token.AccessToken)
						xbmc.SetSetting("trakt_refresh_token", token.RefreshToken)
						log.Noticef("Token refreshed for Trakt authorization, next refresh in %s", time.Duration(token.ExpiresIn-259200)*time.Second)
					}
				} else {
					err = fmt.Errorf("Bad status while refreshing Trakt token: %d", resp.Status())
					xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
					log.Error(err)
				}
			}
		}
	}
}

// Authorize ...
func Authorize(fromSettings bool) error {
	code, err := GetCode()

	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
		return err
	}
	log.Noticef("Got code for %s: %s", code.VerificationURL, code.UserCode)

	if xbmc.Dialog("LOCALIZE[30058]", fmt.Sprintf("Visit %s and enter your code: %s", code.VerificationURL, code.UserCode)) == false {
		return errors.New("Authentication canceled")
	}

	token, err := PollToken(code)
	log.Debugf("Received token: %#v", token)

	if err != nil {
		xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
		return err
	}

	success := "Woohoo!"
	if fromSettings {
		success += " (Save your settings!)"
	}

	expiry := time.Now().Unix() + int64(token.ExpiresIn)
	xbmc.SetSetting("trakt_token_expiry", strconv.Itoa(int(expiry)))
	xbmc.SetSetting("trakt_token", token.AccessToken)
	xbmc.SetSetting("trakt_refresh_token", token.RefreshToken)

	config.Get().TraktToken = token.AccessToken

	// Getting username for currently authorized user
	params := napping.Params{}.AsUrlValues()
	resp, err := GetWithAuth("users/settings", params)
	if resp.Status() == 200 {
		user := &UserSettings{}
		errJSON := resp.Unmarshal(user)
		if errJSON != nil {
			return errJSON
		}

		if user.User.Username != "" {
			xbmc.SetSetting("trakt_username", user.User.Username)
		}
	}

	xbmc.Notify("Elementum", success, config.AddonIcon())
	return nil
}

// Authorized ...
func Authorized() error {
	if config.Get().TraktToken == "" {
		err := Authorize(false)
		if err != nil {
			return err
		}
	}
	return nil
}

// AddToWatchlist ...
func AddToWatchlist(itemType string, tmdbID string) (resp *napping.Response, err error) {
	if err := Authorized(); err != nil {
		return nil, err
	}

	endPoint := "sync/watchlist"
	return Post(endPoint, bytes.NewBufferString(fmt.Sprintf(`{"%s": [{"ids": {"tmdb": %s}}]}`, itemType, tmdbID)))
}

// RemoveFromWatchlist ...
func RemoveFromWatchlist(itemType string, tmdbID string) (resp *napping.Response, err error) {
	if err := Authorized(); err != nil {
		return nil, err
	}

	endPoint := "sync/watchlist/remove"
	return Post(endPoint, bytes.NewBufferString(fmt.Sprintf(`{"%s": [{"ids": {"tmdb": %s}}]}`, itemType, tmdbID)))
}

// AddToCollection ...
func AddToCollection(itemType string, tmdbID string) (resp *napping.Response, err error) {
	if err := Authorized(); err != nil {
		return nil, err
	}

	endPoint := "sync/collection"
	return Post(endPoint, bytes.NewBufferString(fmt.Sprintf(`{"%s": [{"ids": {"tmdb": %s}}]}`, itemType, tmdbID)))
}

// RemoveFromCollection ...
func RemoveFromCollection(itemType string, tmdbID string) (resp *napping.Response, err error) {
	if err := Authorized(); err != nil {
		return nil, err
	}

	endPoint := "sync/collection/remove"
	return Post(endPoint, bytes.NewBufferString(fmt.Sprintf(`{"%s": [{"ids": {"tmdb": %s}}]}`, itemType, tmdbID)))
}

// SetWatched addes and removes from watched history
func SetWatched(item *WatchedItem) (resp *napping.Response, err error) {
	if err := Authorized(); err != nil {
		return nil, err
	}

	pre := `{"movies": [`
	post := `]}`
	if item.Movie == 0 {
		pre = `{"shows": [`
	}

	query := item.String()
	endPoint := "sync/history"
	if !item.Watched {
		endPoint = "sync/history/remove"
	}

	return Post(endPoint, bytes.NewBufferString(pre+query+post))
}

// SetMultipleWatched adds and removes from watched history
func SetMultipleWatched(items []*WatchedItem) (resp *napping.Response, err error) {
	if err := Authorized(); err != nil || len(items) == 0 {
		return nil, err
	}

	pre := `{"movies": [`
	post := `]}`
	if items[0].Movie == 0 {
		pre = `{"shows": [`
	}

	queries := []string{}
	for _, item := range items {
		if item == nil {
			continue
		}
		queries = append(queries, item.String())
	}
	query := strings.Join(queries, ", ")

	endPoint := "sync/history"
	if !items[0].Watched {
		endPoint = "sync/history/remove"
	}

	cache.NewDBStore().Delete(fmt.Sprintf("com.trakt.%ss.watched", items[0].MediaType))

	log.Debugf("Setting watch state for %d %s items", len(items), items[0].MediaType)
	return Post(endPoint, bytes.NewBufferString(pre+query+post))
}

func (item *WatchedItem) String() (query string) {
	watchedAt := fmt.Sprintf(`"watched_at": "%s",`, time.Now().Format("20060102-15:04:05.000"))

	if item.Movie != 0 {
		query = fmt.Sprintf(`{ %s "ids": {"tmdb": %d }}`, watchedAt, item.Movie)
	} else if item.Episode != 0 && item.Season != 0 && item.Show != 0 {
		query = fmt.Sprintf(`{ "ids": {"tmdb": %d}, "seasons": [{ "number": %d, "episodes": [{%s "number": %d }]}]}`, item.Show, item.Season, watchedAt, item.Episode)
	} else if item.Season != 0 && item.Show != 0 {
		query = fmt.Sprintf(`{ "ids": {"tmdb": %d}, "seasons": [{ %s "number": %d }]}`, item.Show, watchedAt, item.Season)
	} else {
		query = fmt.Sprintf(`{ "ids": {"tmdb": %d}}`, item.Show)
	}

	return
}

// This is commented for future use (if needed)
// // SetMultipleWatched addes and removes list from watched history
// func SetMultipleWatched(watched bool, itemType string, tmdbID []string) (resp *napping.Response, err error) {
// 	if err := Authorized(); err != nil {
// 		return nil, err
// 	}
//
// 	endPoint := "sync/history"
// 	if !watched {
// 		endPoint = "sync/history/remove"
// 	}
//
// 	buf := bytes.NewBuffer([]byte(""))
// 	buf.WriteString(fmt.Sprintf(`{"%ss": [`, itemType))
// 	for _, i := range tmdbID {
// 		buf.WriteString(fmt.Sprintf(`{"ids": {"tmdb": %s}}`, i))
// 	}
// 	buf.WriteString(`]}`)
// 	return Post(endPoint, buf)
// }

// Scrobble ...
func Scrobble(action string, contentType string, tmdbID int, watched float64, runtime float64) {
	if err := Authorized(); err != nil {
		return
	}

	if runtime < 1 {
		return
	}
	progress := watched / runtime * 100

	log.Noticef("%s %s: %f%%, watched: %fs, duration: %fs", action, contentType, progress, watched, runtime)

	endPoint := fmt.Sprintf("scrobble/%s", action)
	payload := fmt.Sprintf(`{"%s": {"ids": {"tmdb": %d}}, "progress": %f, "app_version": "%s"}`,
		contentType, tmdbID, progress, util.Version[1:len(util.Version)-1])
	resp, err := Post(endPoint, bytes.NewBufferString(payload))
	if err != nil {
		log.Error(err.Error())
		xbmc.Notify("Elementum", "Scrobble failed, check your logs.", config.AddonIcon())
	} else if resp.Status() != 201 {
		log.Errorf("Failed to scrobble %s #%d to %s at %f: %d", contentType, tmdbID, action, progress, resp.Status())
	}
}
