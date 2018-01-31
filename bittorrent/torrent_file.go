package bittorrent

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/bogdanovich/dns_resolver"
	"github.com/op/go-logging"
	"github.com/zeebo/bencode"

	"github.com/elgatito/elementum/cloudhole"
	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/xbmc"
)

var torrentFileLog = logging.MustGetLogger("torrentFile")

// TorrentFile represents a physical torrent file
type TorrentFile struct {
	URI       string   `json:"uri"`
	InfoHash  string   `json:"info_hash"`
	Name      string   `json:"name"`
	Trackers  []string `json:"trackers"`
	Size      string   `json:"size"`
	Seeds     int64    `json:"seeds"`
	Peers     int64    `json:"peers"`
	IsPrivate bool     `json:"is_private"`
	Provider  string   `json:"provider"`
	Icon      string   `json:"icon"`
	Multi     bool

	Resolution  int    `json:"resolution"`
	VideoCodec  int    `json:"video_codec"`
	AudioCodec  int    `json:"audio_codec"`
	Language    string `json:"language"`
	RipType     int    `json:"rip_type"`
	SceneRating int    `json:"scene_rating"`

	hasResolved bool
}

// Used to avoid infinite recursion in UnmarshalJSON
type torrent TorrentFile

// TorrentFileRaw ...
type TorrentFileRaw struct {
	Announce     string                 `bencode:"announce"`
	AnnounceList [][]string             `bencode:"announce-list"`
	Info         map[string]interface{} `bencode:"info"`
}

const (
	// ResolutionUnknown ...
	ResolutionUnknown = iota
	// Resolution240p ...
	Resolution240p
	// Resolution480p ...
	Resolution480p
	// Resolution720p ...
	Resolution720p
	// Resolution1080p ...
	Resolution1080p
	// Resolution1440p ...
	Resolution1440p
	// Resolution4k ...
	Resolution4k
)

var (
	resolutionTags = map[*regexp.Regexp]int{
		regexp.MustCompile(`\W+240p\W*`):  Resolution240p,
		regexp.MustCompile(`\W+480p\W*`):  Resolution480p,
		regexp.MustCompile(`\W+720p\W*`):  Resolution720p,
		regexp.MustCompile(`\W+1080p\W*`): Resolution1080p,
		regexp.MustCompile(`\W+1440p\W*`): Resolution1440p,
		regexp.MustCompile(`\W+2160p\W*`): Resolution4k,

		regexp.MustCompile(`\W+(tvrip|satrip|vhsrip)\W*`):         Resolution240p,
		regexp.MustCompile(`\W+(xvid|dvd|hdtv|web\-(dl)?rip)\W*`): Resolution480p,
		regexp.MustCompile(`\W+(hdrip|b[rd]rip)\W*`):              Resolution720p,
		regexp.MustCompile(`\W+(fullhd|fhd|blu\W*ray)\W*`):        Resolution1080p,
		regexp.MustCompile(`\W+2K\W*`):                            Resolution1440p,
		regexp.MustCompile(`\W+4K\W*`):                            Resolution4k,
	}
	// Resolutions ...
	Resolutions = []string{"", "240p", "480p", "720p", "1080p", "1440p", "4K"}
	// Colors ...
	Colors = []string{"", "FFFC3401", "FFA56F01", "FF539A02", "FF0166FC", "FFF15052", "FF6BB9EC"}
)

const (
	// RipUnknown ...
	RipUnknown = iota
	// RipCam ...
	RipCam
	// RipTS ...
	RipTS
	// RipTC ...
	RipTC
	// RipScr ...
	RipScr
	// RipDVDScr ...
	RipDVDScr
	// RipDVD ...
	RipDVD
	// RipHDTV ...
	RipHDTV
	// RipWeb ...
	RipWeb
	// RipBluRay ...
	RipBluRay
)

var (
	ripTags = map[*regexp.Regexp]int{
		regexp.MustCompile(`\W+(cam|camrip|hdcam)\W*`):   RipCam,
		regexp.MustCompile(`\W+(ts|telesync)\W*`):        RipTS,
		regexp.MustCompile(`\W+(tc|telecine)\W*`):        RipTC,
		regexp.MustCompile(`\W+(scr|screener)\W*`):       RipScr,
		regexp.MustCompile(`\W+dvd\W*scr\W*`):            RipDVDScr,
		regexp.MustCompile(`\W+dvd\W*rip\W*`):            RipDVD,
		regexp.MustCompile(`\W+hd(tv|rip)\W*`):           RipHDTV,
		regexp.MustCompile(`\W+(web\W*dl|web\W*rip)\W*`): RipWeb,
		regexp.MustCompile(`\W+(bluray|b[rd]rip)\W*`):    RipBluRay,
	}
	// Rips ...
	Rips = []string{"", "Cam", "TeleSync", "TeleCine", "Screener", "DVD Screener", "DVDRip", "HDTV", "WebDL", "Blu-Ray"}
)

const (
	// RatingUnkown ...
	RatingUnkown = iota
	// RatingProper ...
	RatingProper
	// RatingNuked ...
	RatingNuked
)

var (
	sceneTags = map[*regexp.Regexp]int{
		regexp.MustCompile(`\W+nuked\W*`):  RatingNuked,
		regexp.MustCompile(`\W+proper\W*`): RatingProper,
	}
)

const (
	// CodecUnknown ...
	CodecUnknown = iota

	// CodecXVid ...
	CodecXVid
	// CodecH264 ...
	CodecH264
	// CodecH265 ...
	CodecH265

	// CodecMp3 ...
	CodecMp3
	// CodecAAC ...
	CodecAAC
	// CodecAC3 ...
	CodecAC3
	// CodecDTS ...
	CodecDTS
	// CodecDTSHD ...
	CodecDTSHD
	// CodecDTSHDMA ...
	CodecDTSHDMA
)

var (
	videoTags = map[*regexp.Regexp]int{
		regexp.MustCompile(`\W+xvid\W*`):           CodecXVid,
		regexp.MustCompile(`\W+([hx]264)\W*`):      CodecH264,
		regexp.MustCompile(`\W+([hx]265|hevc)\W*`): CodecH265,
	}
	audioTags = map[*regexp.Regexp]int{
		regexp.MustCompile(`\W+mp3\W*`):              CodecMp3,
		regexp.MustCompile(`\W+aac\W*`):              CodecAAC,
		regexp.MustCompile(`\W+(ac3|[Dd]*5\W+1)\W*`): CodecAC3,
		regexp.MustCompile(`\W+dts\W*`):              CodecDTS,
		regexp.MustCompile(`\W+dts\W+hd\W*`):         CodecDTSHD,
		regexp.MustCompile(`\W+dts\W+hd\W+ma\W*`):    CodecDTSHDMA,
	}
	// Codecs ...
	Codecs = []string{"", "Xvid", "H.264", "H.265", "MP3", "AAC", "AC3", "DTS", "DTS HD", "DTS HD MA"}
)

var (
	dnsCache        sync.Map
	resolverPublic  = dns_resolver.New([]string{"8.8.8.8", "8.8.4.4", "9.9.9.9"})
	resolverOpennic = dns_resolver.New([]string{"193.183.98.66", "172.104.136.243", "89.18.27.167"})

	dialer = &net.Dialer{
		Timeout:   7 * time.Second,
		KeepAlive: 10 * time.Second,
		DualStack: true,
	}
	tr = &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			now := time.Now()
			addrs := strings.Split(addr, ":")
			if len(addrs) == 2 && len(addrs[0]) > 2 && strings.Index(addrs[0], ".") > -1 {
				ipTest := net.ParseIP(addrs[0])
				if ipTest == nil {
					ip := resolveAddr(addrs[0])
					log.Debugf("Resolved %s to %s in %s", addrs[0], ip, time.Since(now))
					if ip != "" {
						addr = ip + ":" + addrs[1]
					}
				}
			}
			return dialer.DialContext(ctx, network, addr)
		},
	}
	httpClient = &http.Client{
		Transport: tr,
		Timeout:   7 * time.Second,
	}
)

const (
	xtPrefix = "urn:btih:"
	torCache = "http://itorrents.org/torrent/%s.torrent"
)

func resolveAddr(host string) (ip string) {
	if cached, ok := dnsCache.Load(host); ok {
		return cached.(string)
	}

	defer func() {
		if strings.HasPrefix(ip, "127.") {
			return
		}

		dnsCache.Store(host, ip)
	}()

	ips, err := resolverPublic.LookupHost(host)
	if err == nil && len(ips) > 0 {
		ip = ips[0].String()
		return
	}

	if cached, ok := dnsCache.Load(host); ok {
		return cached.(string)
	}

	ips, err = resolverOpennic.LookupHost(host)
	if err == nil && len(ips) > 0 {
		ip = ips[0].String()
		return
	}

	return
}

// UnmarshalJSON ...
func (t *TorrentFile) UnmarshalJSON(b []byte) error {
	tmp := torrent{}
	if err := json.Unmarshal(b, &tmp); err != nil {
		return err
	}
	*t = TorrentFile(tmp)
	t.initialize()
	return nil
}

// MarshalJSON ...
func (t *TorrentFile) MarshalJSON() ([]byte, error) {
	tmp := torrent(*t)
	log.Debugf("Marshalling: %#v", tmp)
	b, err := json.Marshal(tmp)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// IsMagnet ...
func (t *TorrentFile) IsMagnet() bool {
	return strings.HasPrefix(t.URI, "magnet:")
}

// IsValidMagnet Taken from anacrolix/torrent
func (t *TorrentFile) IsValidMagnet() (err error) {
	u, err := url.Parse(t.URI)
	if err != nil {
		err = fmt.Errorf("error parsing uri: %s", err)
		return
	}
	if u.Scheme != "magnet" {
		err = fmt.Errorf("unexpected scheme: %q", u.Scheme)
		return
	}
	xt := u.Query().Get("xt")
	if !strings.HasPrefix(xt, xtPrefix) {
		err = fmt.Errorf("bad xt parameter")
		return
	}
	infoHash := xt[len(xtPrefix):]

	// BTIH hash can be in HEX or BASE32 encoding
	// will assign appropriate func judging from symbol length
	var decode func(dst, src []byte) (int, error)
	switch len(infoHash) {
	case 40:
		decode = hex.Decode
	case 32:
		decode = base32.StdEncoding.Decode
	}

	if decode == nil {
		err = fmt.Errorf("unhandled xt parameter encoding: encoded length %d", len(infoHash))
		return
	}
	n, err := decode([]byte(t.InfoHash)[:], []byte(infoHash))
	if err != nil {
		err = fmt.Errorf("error decoding xt: %s", err)
		return
	}
	if n != 20 {
		err = fmt.Errorf("invalid magnet length: %d", n)
		return
	}
	return
}

// NewTorrentFile ...
func NewTorrentFile(uri string) *TorrentFile {
	t := &TorrentFile{
		URI: uri,
	}
	t.initialize()
	return t
}

func (t *TorrentFile) initialize() {
	if t.IsMagnet() {
		t.initializeFromMagnet()
	}

	if t.Resolution == ResolutionUnknown {
		t.Resolution = matchLowerTags(t, resolutionTags)
		if t.Resolution == ResolutionUnknown {
			t.Resolution = Resolution480p
		}
	}
	if t.VideoCodec == CodecUnknown {
		t.VideoCodec = matchTags(t, videoTags)
	}
	if t.AudioCodec == CodecUnknown {
		t.AudioCodec = matchTags(t, audioTags)
	}
	if t.RipType == RipUnknown {
		t.RipType = matchTags(t, ripTags)
	}
	if t.SceneRating == RatingUnkown {
		t.SceneRating = matchTags(t, sceneTags)
	}
}

func (t *TorrentFile) initializeFromMagnet() {
	magnetURI, _ := url.Parse(t.URI)
	vals := magnetURI.Query()
	hash := strings.ToUpper(strings.TrimPrefix(vals.Get("xt"), "urn:btih:"))

	// for backward compatibility
	if unBase32Hash, err := base32.StdEncoding.DecodeString(hash); err == nil {
		hash = hex.EncodeToString(unBase32Hash)
	}

	if t.InfoHash == "" {
		t.InfoHash = strings.ToLower(hash)
	}
	if t.Name == "" {
		t.Name = vals.Get("dn")
	}

	if len(t.Trackers) == 0 {
		t.Trackers = make([]string, 0)
		for _, tracker := range vals["tr"] {
			t.Trackers = append(t.Trackers, strings.Replace(string(tracker), "\\", "", -1))
		}
	}
}

// Magnet ...
func (t *TorrentFile) Magnet() {
	if t.hasResolved == false {
		t.Resolve()
	}

	params := url.Values{}
	params.Set("dn", t.Name)
	if len(t.Trackers) == 0 {
		for _, tracker := range DefaultTrackers {
			params.Add("tr", tracker)
		}
	} else {
		for _, tracker := range t.Trackers {
			params.Add("tr", tracker)
		}
	}

	t.URI = fmt.Sprintf("magnet:?xt=urn:btih:%s&%s", t.InfoHash, params.Encode())

	if t.IsValidMagnet() == nil {
		params.Add("as", t.URI)
	} else {
		params.Add("as", fmt.Sprintf(torCache, t.InfoHash))
	}
}

// LoadFromBytes ...
func (t *TorrentFile) LoadFromBytes(in []byte) error {

	var torrentFile *TorrentFileRaw
	if err := bencode.DecodeBytes(in, &torrentFile); err != nil {
		return err
	}

	if t.InfoHash == "" {
		hasher := sha1.New()
		bencode.NewEncoder(hasher).Encode(torrentFile.Info)
		t.InfoHash = hex.EncodeToString(hasher.Sum(nil))
	}

	if t.Name == "" {
		t.Name = torrentFile.Info["name"].(string)
	}

	if torrentFile.Info["private"] != nil {
		if torrentFile.Info["private"].(int64) == 1 {
			torrentFileLog.Noticef("%s marked as private", t.Name)
			t.IsPrivate = true
		}
	}

	if len(t.Trackers) == 0 {
		t.Trackers = append(t.Trackers, torrentFile.Announce)
		for _, trackers := range torrentFile.AnnounceList {
			t.Trackers = append(t.Trackers, trackers...)
		}
	}

	// Save torrent file in temp folder
	torrentFileName := filepath.Join(config.Get().Info.TempPath, fmt.Sprintf("%s.torrent", t.InfoHash))
	out, err := os.Create(torrentFileName)
	if err != nil {
		return err
	}
	defer func() {
		cerr := out.Close()
		if err == nil {
			err = cerr
		}
	}()
	buf := bytes.NewReader(in)
	if _, err := io.Copy(out, buf); err != nil {
		return err
	}
	t.URI = torrentFileName

	t.hasResolved = true

	t.initialize()

	return nil
}

// Resolve ...
func (t *TorrentFile) Resolve() error {
	if t.IsMagnet() {
		t.hasResolved = true
		return nil
	}

	parts := strings.Split(t.URI, "|")
	uri := parts[0]
	req, err := http.NewRequest("GET", uri, nil)
	if err != nil {
		return err
	}
	if len(parts) > 1 {
		for _, part := range parts[1:] {
			keyVal := strings.SplitN(part, "=", 2)
			req.Header.Add(keyVal[0], keyVal[1])
		}
	}

	// Use CloudHole if enabled and if we have a clearance
	if config.Get().UseCloudHole == true {
		clearance, _ := cloudhole.GetClearance()
		if clearance.Cookies != "" {
			req.Header.Set("User-Agent", clearance.UserAgent)
			if cookies := req.Header.Get("Cookie"); cookies != "" {
				req.Header.Set("Cookie", cookies+"; "+clearance.Cookies)
			} else {
				req.Header.Add("Cookie", clearance.Cookies)
			}
		}
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	tee := io.TeeReader(resp.Body, &buf)
	dec := bencode.NewDecoder(tee)

	var torrentFile *TorrentFileRaw

	if errDec := dec.Decode(&torrentFile); errDec != nil {
		return errDec
	}

	if t.InfoHash == "" {
		hasher := sha1.New()
		bencode.NewEncoder(hasher).Encode(torrentFile.Info)
		t.InfoHash = hex.EncodeToString(hasher.Sum(nil))
	}

	if t.Name == "" {
		t.Name = torrentFile.Info["name"].(string)
	}

	if torrentFile.Info["private"] != nil {
		if torrentFile.Info["private"].(int64) == 1 {
			torrentFileLog.Noticef("%s marked as private", t.Name)
			t.IsPrivate = true
		}
	}

	if len(t.Trackers) == 0 {
		t.Trackers = append(t.Trackers, torrentFile.Announce)
		for _, trackers := range torrentFile.AnnounceList {
			t.Trackers = append(t.Trackers, trackers...)
		}
	}

	// Save torrent file in temp folder
	torrentFileName := filepath.Join(config.Get().Info.TempPath, fmt.Sprintf("%s.torrent", t.InfoHash))
	out, err := os.Create(torrentFileName)
	if err != nil {
		return err
	}
	defer func() {
		cerr := out.Close()
		if err == nil {
			err = cerr
		}
	}()
	if _, err := io.Copy(out, &buf); err != nil {
		return err
	}
	t.URI = torrentFileName

	t.hasResolved = true

	t.initialize()

	return nil
}

func matchTags(t *TorrentFile, tokens map[*regexp.Regexp]int) int {
	lowName := strings.ToLower(t.Name)
	codec := 0
	for re, value := range tokens {
		if re.MatchString(lowName) {
			// TODO: Do wee need to match for upper scale?
			// is 720p is matched then it's not 1080p for sure?!
			if value > codec {
				codec = value
			}
		}
	}
	return codec
}

func matchLowerTags(t *TorrentFile, tokens map[*regexp.Regexp]int) int {
	lowName := strings.ToLower(t.Name)
	for re, value := range tokens {
		if re.MatchString(lowName) {
			return value
		}
	}
	return 0
}

// StreamInfo ...
func (t *TorrentFile) StreamInfo() *xbmc.StreamInfo {
	sie := &xbmc.StreamInfo{
		Video: &xbmc.StreamInfoEntry{
			Codec: Codecs[t.VideoCodec],
		},
		Audio: &xbmc.StreamInfoEntry{
			Codec: Codecs[t.AudioCodec],
		},
	}

	switch t.Resolution {
	case Resolution480p:
		sie.Video.Width = 853
		sie.Video.Height = 480
		break
	case Resolution720p:
		sie.Video.Width = 1280
		sie.Video.Height = 720
		break
	case Resolution1080p:
		sie.Video.Width = 1920
		sie.Video.Height = 1080
		break
	case Resolution1440p:
		sie.Video.Width = 2560
		sie.Video.Height = 1440
		break
	case Resolution4k:
		sie.Video.Width = 4096
		sie.Video.Height = 2160
		break
	}

	return sie
}
