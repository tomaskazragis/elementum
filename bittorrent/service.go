package bittorrent

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/anacrolix/dht"
	gotorrent "github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/iplist"
	"github.com/anacrolix/torrent/storage"
	"github.com/dustin/go-humanize"
	"github.com/op/go-logging"
	"golang.org/x/time/rate"

	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/database"
	"github.com/elgatito/elementum/diskusage"
	estorage "github.com/elgatito/elementum/storage"
	memory "github.com/elgatito/elementum/storage/memory"
	"github.com/elgatito/elementum/tmdb"
	"github.com/elgatito/elementum/util"
	"github.com/elgatito/elementum/xbmc"
)

const (
	Delete = iota
	Update
	RemoveFromLibrary
)

const (
	Remove = iota
	Active
)

// const (
// 	ipToSDefault     = iota
// 	ipToSLowDelay    = 1 << iota
// 	ipToSReliability = 1 << iota
// 	ipToSThroughput  = 1 << iota
// 	ipToSLowCost     = 1 << iota
// )

var DefaultTrackers = []string{
	"udp://tracker.opentrackr.org:1337/announce",
	"udp://tracker.coppersurfer.tk:6969/announce",
	"udp://tracker.leechers-paradise.org:6969/announce",
	"udp://tracker.openbittorrent.com:80/announce",
	"udp://public.popcorn-tracker.org:6969/announce",
	"udp://explodie.org:6969",
}

var (
	db     *database.Database
	Bucket = database.BitTorrentBucket
)

// const (
// 	ProxyTypeNone = iota
// 	ProxyTypeSocks4
// 	ProxyTypeSocks5
// 	ProxyTypeSocks5Password
// 	ProxyTypeSocksHTTP
// 	ProxyTypeSocksHTTPPassword
// 	ProxyTypeI2PSAM
// )

// type ProxySettings struct {
// 	Type     int
// 	Port     int
// 	Hostname string
// 	Username string
// 	Password string
// }

type BTConfiguration struct {
	SpoofUserAgent      int
	DownloadStorage     int
	MemorySize          int64
	BufferSize          int64
	MaxUploadRate       int
	MaxDownloadRate     int
	LimitAfterBuffering bool
	ConnectionsLimit    int
	SeedTimeLimit       int
	DisableDHT          bool
	DisableTCP          bool
	DisableUTP          bool
	EncryptionPolicy    int
	LowerListenPort     int
	UpperListenPort     int
	ListenInterfaces    string
	OutgoingInterfaces  string
	DownloadPath        string
	TorrentsPath        string
	DisableBgProgress   bool
	CompletedMove       bool
	CompletedMoviesPath string
	CompletedShowsPath  string

	// SessionSave         int
	// ShareRatioLimit     int
	// SeedTimeRatioLimit  int
	// DisableUPNP         bool
	// TunedStorage        bool
	// Proxy               *ProxySettings
}

type BTService struct {
	Client          *gotorrent.Client
	ClientConfig    *gotorrent.Config
	PieceCompletion storage.PieceCompletion
	DefaultStorage  estorage.ElementumStorage
	// StorageEvents   *pubsub.PubSub
	DownloadLimiter *rate.Limiter
	UploadLimiter   *rate.Limiter
	Torrents        map[string]*Torrent
	UserAgent       string

	config           *BTConfiguration
	log              *logging.Logger
	dialogProgressBG *xbmc.DialogProgressBG
	SpaceChecked     map[string]bool
	MarkedToMove     string

	// closing      chan struct{}
	// bufferEvents chan int
	// pieceEvents  chan qstorage.PieceChange
}

type DBItem struct {
	ID      int    `json:"id"`
	State   int    `json:"state"`
	Type    string `json:"type"`
	File    int    `json:"file"`
	ShowID  int    `json:"showid"`
	Season  int    `json:"season"`
	Episode int    `json:"episode"`
}

type PlayingItem struct {
	DBID   int
	DBTYPE string

	TMDBID  int
	Season  int
	Episode int

	WatchedTime float64
	Duration    float64
}

type activeTorrent struct {
	torrentName  string
	downloadRate float64
	uploadRate   float64
	progress     int
}

func InitDB() {
	db, _ = database.NewDB()
}

func NewBTService(conf BTConfiguration) *BTService {
	s := &BTService{
		log:    logging.MustGetLogger("btservice"),
		config: &conf,

		SpaceChecked: make(map[string]bool, 0),
		MarkedToMove: "",
		// StorageEvents: pubsub.NewPubSub(),

		Torrents: map[string]*Torrent{},

		// TODO: cleanup when limiting is finished
		DownloadLimiter: rate.NewLimiter(rate.Inf, 2<<18),
		UploadLimiter:   rate.NewLimiter(rate.Inf, 2<<17),
		// DownloadLimiter: rate.NewLimiter(rate.Inf, 0),
		// UploadLimiter:   rate.NewLimiter(rate.Inf, 0),

		// DownloadLimiter: rate.NewLimiter(rate.Inf, 2 << 19),
		// UploadLimiter:   rate.NewLimiter(rate.Inf, 2 << 19),
		// DownloadLimiter: rate.NewLimiter(rate.Inf, 1<<20),
		// UploadLimiter:   rate.NewLimiter(rate.Inf, 256<<10),
		// DownloadLimiter: rate.NewLimiter(rate.Inf, 1),
		// UploadLimiter:   rate.NewLimiter(rate.Inf, 1),
		// DownloadLimiter: rate.NewLimiter(rate.Inf, 0),
		// UploadLimiter:   rate.NewLimiter(rate.Inf, 0),
	}

	if _, err := os.Stat(s.config.TorrentsPath); os.IsNotExist(err) {
		if err := os.Mkdir(s.config.TorrentsPath, 0755); err != nil {
			s.log.Error("Unable to create Torrents folder")
		}
	}

	if completion, err := storage.NewBoltPieceCompletion(config.Get().ProfilePath); err == nil {
		s.PieceCompletion = completion
	} else {
		s.PieceCompletion = storage.NewMapPieceCompletion()
	}

	s.configure()

	tmdb.CheckApiKey()

	go s.loadTorrentFiles()
	go s.downloadProgress()

	return s
}

func (s *BTService) Close(shutdown bool) {
	s.log.Info("Stopping BT Services...")
	s.stopServices(shutdown)
	s.Client.Close()
}

func (s *BTService) Reconfigure(config BTConfiguration) {
	s.stopServices(false)
	s.config = &config
	s.configure()
	s.loadTorrentFiles()
}

func (s *BTService) configure() {
	s.log.Info("Configuring client...")

	var listenPorts []string
	for p := s.config.UpperListenPort; p >= s.config.LowerListenPort; p-- {
		ln, err := net.Listen("tcp", ":"+strconv.Itoa(p))
		if err != nil {
			continue
		}

		ln.Close()
		listenPorts = append(listenPorts, strconv.Itoa(p))
	}
	rand.Seed(time.Now().UTC().UnixNano())

	listenInterfaces := []string{"0.0.0.0"}
	if strings.TrimSpace(s.config.ListenInterfaces) != "" {
		listenInterfaces = strings.Split(strings.Replace(strings.TrimSpace(s.config.ListenInterfaces), " ", "", -1), ",")
	}

	listenInterfacesStrings := make([]string, 0)
	for _, listenInterface := range listenInterfaces {
		listenInterfacesStrings = append(listenInterfacesStrings, listenInterface+":"+listenPorts[rand.Intn(len(listenPorts))])
		if len(listenPorts) > 1 {
			listenInterfacesStrings = append(listenInterfacesStrings, listenInterface+":"+listenPorts[rand.Intn(len(listenPorts))])
		}
	}

	blocklist, err := iplist.MMapPacked("packed-blocklist")
	if err != nil {
		s.log.Debug(err)
	}

	userAgent := util.UserAgent()
	if s.config.SpoofUserAgent > 0 {
		switch s.config.SpoofUserAgent {
		case 1:
			userAgent = ""
			break
		case 2:
			userAgent = "libtorrent (Rasterbar) 1.1.0"
			break
		case 3:
			userAgent = "BitTorrent 7.5.0"
			break
		case 4:
			userAgent = "BitTorrent 7.4.3"
			break
		case 5:
			userAgent = "µTorrent 3.4.9"
			break
		case 6:
			userAgent = "µTorrent 3.2.0"
			break
		case 7:
			userAgent = "µTorrent 2.2.1"
			break
		case 8:
			userAgent = "Transmission 2.92"
			break
		case 9:
			userAgent = "Deluge 1.3.6.0"
			break
		case 10:
			userAgent = "Deluge 1.3.12.0"
			break
		case 11:
			userAgent = "Vuze 5.7.3.0"
			break
		}
		if userAgent != "" {
			s.log.Infof("UserAgent: %s", userAgent)
		}
	} else {
		s.log.Infof("UserAgent: %s", util.UserAgent())
	}

	if userAgent != "" {
		s.UserAgent = userAgent
	}

	if s.config.ConnectionsLimit == 0 {
		setPlatformSpecificSettings(s.config)
	}

	if s.config.MaxDownloadRate == 0 {
		s.DownloadLimiter = rate.NewLimiter(rate.Inf, 0)
	}
	if s.config.MaxUploadRate == 0 {
		s.UploadLimiter = rate.NewLimiter(rate.Inf, 0)
	}

	s.log.Infof("DownloadStorage: %d", s.config.DownloadStorage)
	if s.config.DownloadStorage == estorage.StorageMemory {
		// Forcing disable upload for memory storage
		s.config.SeedTimeLimit = 0

		memSize := int64(config.Get().MemorySize)
		needSize := s.config.BufferSize + endBufferSize + 6*1024*1024

		if memSize < needSize {
			s.log.Noticef("Raising memory size (%d) to fit all the buffer (%d)", memSize, needSize)
			memSize = needSize
		}

		s.config.SeedTimeLimit = 0
		s.DefaultStorage = memory.NewMemoryStorage(memSize)
	} else if s.config.DownloadStorage == estorage.StorageFat32 {
		s.DefaultStorage = estorage.NewFat32Storage(config.Get().DownloadPath)
	} else if s.config.DownloadStorage == estorage.StorageMMap {
		s.DefaultStorage = estorage.NewMMapStorage(config.Get().DownloadPath, s.PieceCompletion)
	} else {
		s.DefaultStorage = estorage.NewFileStorage(config.Get().DownloadPath, s.PieceCompletion)
	}
	s.DefaultStorage.SetReadaheadSize(s.GetBufferSize())

	// TODO: Forcing no upload for the moment
	s.config.SeedTimeLimit = 0
	s.ClientConfig = &gotorrent.Config{
		DataDir: config.Get().DownloadPath,

		ListenAddr: listenInterfacesStrings[0],
		Debug:      true,

		DisableTCP: s.config.DisableTCP,
		DisableUTP: s.config.DisableUTP,

		NoDHT: s.config.DisableDHT,
		DHTConfig: dht.ServerConfig{
			StartingNodes: dht.GlobalBootstrapAddrs,
		},

		Seed:     s.config.SeedTimeLimit > 0,
		NoUpload: s.config.SeedTimeLimit == 0,

		EncryptionPolicy: gotorrent.EncryptionPolicy{
			DisableEncryption: s.config.EncryptionPolicy == 0,
			ForceEncryption:   s.config.EncryptionPolicy == 2,
		},

		IPBlocklist: blocklist,

		DownloadRateLimiter: s.DownloadLimiter,
		UploadRateLimiter:   s.UploadLimiter,

		DefaultStorage: s.DefaultStorage,
	}

	if !s.config.LimitAfterBuffering {
		s.RestoreLimits()
	}

	s.log.Debugf("BitClient config: %#v", s.ClientConfig)
	if s.Client, err = gotorrent.NewClient(s.ClientConfig); err != nil {
		s.log.Warningf("Error creating bit client: %#v", err)
	} else {
		s.log.Debugf("Created bit client: %#v", s.Client)
	}
}

func (s *BTService) stopServices(shutdown bool) {
	// TODO: cleanup these messages after windows hang is fixed
	// Don't need to execute RPC calls when Kodi is closing
	if !shutdown {
		if s.dialogProgressBG != nil {
			s.dialogProgressBG.Close()
		}
		s.dialogProgressBG = nil
		s.log.Debugf("Cleaning up DialogBG")
		xbmc.DialogProgressBGCleanup()
		s.log.Debugf("Resetting RPC")
		xbmc.ResetRPC()
	}

	s.log.Debugf("Closing Client")
	if s.Client != nil {
		s.Client.Close()
	}
}

// func (s *BTService) Watch() {
// 	defer close(s.closing)
// 	// defer s.StorageChanges.Close()
//
// 	for {
// 		select {
// 		// case change := <- s.pieceEvents:
// 		// 	//change := _i.(qstorage.PieceChange)
// 		// 	log.Debugf("Sending piece change event: %#v", change)
// 		// 	if change.State == "complete" {
// 		// 		s.Torrents[0].Torrent.UpdatePieceCompletion(change.Index)
// 		// 	}
// 		case <- s.closing:
// 			return
// 		}
// 	}
// }

func (s *BTService) CheckAvailableSpace(torrent *Torrent) bool {
	diskStatus := &diskusage.DiskStatus{}
	if status, err := diskusage.DiskUsage(config.Get().DownloadPath); err != nil {
		s.log.Warningf("Unable to retrieve the free space for %s, continuing anyway...", config.Get().DownloadPath)
		return false
	} else {
		diskStatus = status
	}

	if torrent == nil || torrent.Info() == nil {
		s.log.Warning("Missing torrent info to check available space.")
		return false
	}

	totalSize := torrent.BytesCompleted() + torrent.BytesMissing()
	totalDone := torrent.BytesCompleted()
	sizeLeft := torrent.BytesMissing()
	availableSpace := diskStatus.Free
	path := s.ClientConfig.DataDir

	if torrent.IsRarArchive {
		sizeLeft = sizeLeft * 2
	}

	s.log.Infof("Checking for sufficient space on %s...", path)
	s.log.Infof("Total size of download: %s", humanize.Bytes(uint64(totalSize)))
	s.log.Infof("All time download: %s", humanize.Bytes(uint64(torrent.BytesCompleted())))
	s.log.Infof("Size total done: %s", humanize.Bytes(uint64(totalDone)))
	if torrent.IsRarArchive {
		s.log.Infof("Size left to download (x2 to extract): %s", humanize.Bytes(uint64(sizeLeft)))
	} else {
		s.log.Infof("Size left to download: %s", humanize.Bytes(uint64(sizeLeft)))
	}
	s.log.Infof("Available space: %s", humanize.Bytes(uint64(availableSpace)))

	if availableSpace < sizeLeft {
		s.log.Errorf("Unsufficient free space on %s. Has %d, needs %d.", path, diskStatus.Free, sizeLeft)
		xbmc.Notify("Elementum", "LOCALIZE[30207]", config.AddonIcon())

		torrent.Pause()
		return false
	}

	return true
}

func (s *BTService) AddTorrent(uri string) (*Torrent, error) {
	s.log.Infof("Adding torrent from %s", uri)

	if s.config.DownloadPath == "." {
		xbmc.Notify("Elementum", "LOCALIZE[30113]", config.AddonIcon())
		return nil, fmt.Errorf("Download path empty")
	}

	var err error
	var torrentHandle *gotorrent.Torrent
	if strings.HasPrefix(uri, "magnet:") {
		if torrentHandle, err = s.Client.AddMagnet(uri); err != nil {
			return nil, err
		} else if torrentHandle == nil {
			return nil, errors.New("Could not add torrent")
		}
		uri = ""
	} else {
		if strings.HasPrefix(uri, "http") {
			torrent := NewTorrentFile(uri)

			if err = torrent.Resolve(); err != nil {
				s.log.Warningf("Could not resolve torrent %s: %#v", uri, err)
				return nil, err
			}
			uri = torrent.URI
		}

		s.log.Debugf("Adding torrent: %#v", uri)
		s.log.Debugf("Service: %#v", s)
		s.log.Debugf("Client: %#v", s.Client)
		if torrentHandle, err = s.Client.AddTorrentFromFile(uri); err != nil {
			s.log.Warningf("Could not add torrent %s: %#v", uri, err)
			return nil, err
		} else if torrentHandle == nil {
			return nil, errors.New("Could not add torrent")
		}
	}

	log.Debugf("Making new torrent item: %#v", uri)
	torrent := NewTorrent(s, torrentHandle, uri)
	if s.config.ConnectionsLimit > 0 {
		torrentHandle.SetMaxEstablishedConns(s.config.ConnectionsLimit)
	}

	s.Torrents[torrent.infoHash] = torrent

	go torrent.Watch()

	return torrent, nil
}

func (s *BTService) RemoveTorrent(torrent *Torrent, removeFiles bool) bool {
	s.log.Debugf("Removing torrent: %s", torrent.Name())
	if torrent == nil {
		return false
	}

	if t, ok := s.Torrents[torrent.infoHash]; ok {
		delete(s.Torrents, torrent.infoHash)
		t.Drop(removeFiles)
		return true
	}

	// query := torrent.InfoHash()
	// matched := -1
	// for i := range s.Torrents {
	// 	if s.Torrents[i] == query {
	// 		matched = i
	// 		break
	// 	}
	// }
	//
	// if matched > -1 {
	// 	t := s.Torrents[matched]
	//
	// 	go func() {
	// 		t.Drop(removeFiles)
	// 		t = nil
	// 	}()
	//
	// 	s.Torrents = append(s.Torrents[:matched], s.Torrents[matched+1:]...)
	// 	return true
	// }

	return false
}

func (s *BTService) loadTorrentFiles() {
	pattern := filepath.Join(s.config.TorrentsPath, "*.torrent")
	files, _ := filepath.Glob(pattern)

	for _, torrentFile := range files {
		s.log.Infof("Loading torrent file %s", torrentFile)

		torrentHandle := &gotorrent.Torrent{}
		var err error
		if torrentHandle, err = s.Client.AddTorrentFromFile(torrentFile); err != nil || torrentHandle == nil {
			s.log.Errorf("Error adding torrent file for %s", torrentFile)
			if _, err := os.Stat(torrentFile); err == nil {
				if err := os.Remove(torrentFile); err != nil {
					s.log.Error(err)
				}
			}

			continue
		}

		torrent := NewTorrent(s, torrentHandle, torrentFile)

		s.Torrents[torrent.infoHash] = torrent
	}
}

func (s *BTService) downloadProgress() {
	rotateTicker := time.NewTicker(5 * time.Second)
	defer rotateTicker.Stop()

	pathChecked := make(map[string]bool)
	warnedMissing := make(map[string]bool)

	showNext := 0
	for {
		select {
		case <-rotateTicker.C:
			if !s.config.DisableBgProgress && s.dialogProgressBG != nil {
				s.dialogProgressBG.Close()
				s.dialogProgressBG = nil
				continue
			}

			var totalDownloadRate int64
			var totalUploadRate int64
			var totalProgress int

			activeTorrents := make([]*activeTorrent, 0)

			for i, torrentHandle := range s.Torrents {
				if torrentHandle == nil {
					continue
				}

				torrentName := torrentHandle.Info().Name
				progress := int(torrentHandle.GetProgress())
				status := torrentHandle.GetState()

				totalDownloadRate += torrentHandle.DownloadRate
				totalUploadRate += torrentHandle.UploadRate

				if progress < 100 && status != STATUS_PAUSED {
					activeTorrents = append(activeTorrents, &activeTorrent{
						torrentName:  torrentName,
						downloadRate: float64(torrentHandle.DownloadRate),
						uploadRate:   float64(torrentHandle.UploadRate),
						progress:     progress,
					})
					totalProgress += progress
					continue
				}

				if s.MarkedToMove != "" && i == s.MarkedToMove {
					s.MarkedToMove = ""
					status = STATUS_SEEDING
				}

				//
				// Handle moving completed downloads
				//
				if !s.config.CompletedMove || status != STATUS_SEEDING || Playing {
					continue
				}
				if xbmc.PlayerIsPlaying() {
					continue
				}

				infoHash := torrentHandle.InfoHash()
				if _, exists := warnedMissing[infoHash]; exists {
					continue
				}

				item := &DBItem{}
				func() error {
					if err := db.GetObject(Bucket, infoHash, item); err != nil {
						warnedMissing[infoHash] = true
						return err
					}

					errMsg := fmt.Sprintf("Missing item type to move files to completed folder for %s", torrentName)
					if item.Type == "" {
						s.log.Error(errMsg)
						return errors.New(errMsg)
					} else {
						s.log.Warning(torrentName, "finished seeding, moving files...")

						// Check paths are valid and writable, and only once
						if _, exists := pathChecked[item.Type]; !exists {
							if item.Type == "movie" {
								if err := config.IsWritablePath(s.config.CompletedMoviesPath); err != nil {
									warnedMissing[infoHash] = true
									pathChecked[item.Type] = true
									s.log.Error(err)
									return err
								}
								pathChecked[item.Type] = true
							} else {
								if err := config.IsWritablePath(s.config.CompletedShowsPath); err != nil {
									warnedMissing[infoHash] = true
									pathChecked[item.Type] = true
									s.log.Error(err)
									return err
								}
								pathChecked[item.Type] = true
							}
						}

						s.log.Info("Removing the torrent without deleting files...")
						s.RemoveTorrent(torrentHandle, false)

						// Delete torrent file
						torrentFile := filepath.Join(s.config.TorrentsPath, fmt.Sprintf("%s.torrent", infoHash))
						if _, err := os.Stat(torrentFile); err == nil {
							s.log.Info("Deleting torrent file at ", torrentFile)
							if err := os.Remove(torrentFile); err != nil {
								s.log.Error(err)
								return err
							}
						}

						filePath := torrentHandle.Files()[item.File].Path()
						fileName := filepath.Base(filePath)

						extracted := ""
						re := regexp.MustCompile("(?i).*\\.rar")
						if re.MatchString(fileName) {
							extractedPath := filepath.Join(s.config.DownloadPath, filepath.Dir(filePath), "extracted")
							files, err := ioutil.ReadDir(extractedPath)
							if err != nil {
								return err
							}
							if len(files) == 1 {
								extracted = files[0].Name()
							} else {
								for _, file := range files {
									fileName := file.Name()
									re := regexp.MustCompile("(?i).*\\.(mkv|mp4|mov|avi)")
									if re.MatchString(fileName) {
										extracted = fileName
										break
									}
								}
							}
							if extracted != "" {
								filePath = filepath.Join(filepath.Dir(filePath), "extracted", extracted)
							} else {
								return errors.New("No extracted file to move")
							}
						}

						var dstPath string
						if item.Type == "movie" {
							dstPath = filepath.Dir(s.config.CompletedMoviesPath)
						} else {
							dstPath = filepath.Dir(s.config.CompletedShowsPath)
							if item.ShowID > 0 {
								show := tmdb.GetShow(item.ShowID, "en")
								if show != nil {
									showPath := util.ToFileName(fmt.Sprintf("%s (%s)", show.Name, strings.Split(show.FirstAirDate, "-")[0]))
									seasonPath := filepath.Join(showPath, fmt.Sprintf("Season %d", item.Season))
									if item.Season == 0 {
										seasonPath = filepath.Join(showPath, "Specials")
									}
									dstPath = filepath.Join(dstPath, seasonPath)
									os.MkdirAll(dstPath, 0755)
								}
							}
						}

						go func() {
							s.log.Infof("Moving %s to %s", fileName, dstPath)
							srcPath := filepath.Join(s.config.DownloadPath, filePath)
							if dst, err := util.Move(srcPath, dstPath); err != nil {
								s.log.Error(err)
							} else {
								// Remove leftover folders
								if dirPath := filepath.Dir(filePath); dirPath != "." {
									os.RemoveAll(filepath.Dir(srcPath))
									if extracted != "" {
										parentPath := filepath.Clean(filepath.Join(filepath.Dir(srcPath), ".."))
										if parentPath != "." && parentPath != s.config.DownloadPath {
											os.RemoveAll(parentPath)
										}
									}
								}
								s.log.Warning(fileName, "moved to", dst)

								s.log.Infof("Marking %s for removal from library and database...", torrentName)
								s.UpdateDB(RemoveFromLibrary, infoHash, 0, "")
							}
						}()
					}
					return nil
				}()
			}

			totalActive := len(activeTorrents)
			if totalActive > 0 {
				showProgress := totalProgress / totalActive
				showTorrent := fmt.Sprintf("Total - D/L: %s - U/L: %s", humanize.Bytes(uint64(totalDownloadRate))+"/s", humanize.Bytes(uint64(totalUploadRate))+"/s")
				if showNext >= totalActive {
					showNext = 0
				} else {
					showProgress = activeTorrents[showNext].progress
					torrentName := activeTorrents[showNext].torrentName
					if len(torrentName) > 30 {
						torrentName = torrentName[:30] + "..."
					}
					showTorrent = fmt.Sprintf("%s - %s - %s", torrentName, humanize.Bytes(uint64(activeTorrents[showNext].downloadRate))+"/s", humanize.Bytes(uint64(activeTorrents[showNext].uploadRate))+"/s")
					showNext += 1
				}
				if !s.config.DisableBgProgress {
					if s.dialogProgressBG == nil {
						s.dialogProgressBG = xbmc.NewDialogProgressBG("Elementum", "")
					}
					s.dialogProgressBG.Update(showProgress, "Elementum", showTorrent)
				}
			} else if !s.config.DisableBgProgress && s.dialogProgressBG != nil {
				s.dialogProgressBG.Close()
				s.dialogProgressBG = nil
			}
		}
	}
}

//
// Database updates
//
func (s *BTService) UpdateDB(Operation int, InfoHash string, ID int, Type string, infos ...int) error {
	switch Operation {
	case Delete:
		return db.Delete(Bucket, InfoHash)
	case Update:
		item := DBItem{
			State:   Active,
			ID:      ID,
			Type:    Type,
			File:    infos[0],
			ShowID:  infos[1],
			Season:  infos[2],
			Episode: infos[3],
		}
		return db.SetObject(Bucket, InfoHash, item)
	case RemoveFromLibrary:
		item := &DBItem{}
		if err := db.GetObject(Bucket, InfoHash, item); err != nil {
			s.log.Error(err)
			return err
		}

		item.State = Remove
		return db.SetObject(Bucket, InfoHash, item)
	}

	return nil
}

func (s *BTService) GetDBItem(infoHash string) (dbItem *DBItem) {
	if err := db.GetObject(Bucket, infoHash, dbItem); err != nil {
		return nil
	}
	return dbItem
}

func (s *BTService) SetDownloadLimit(i int) {
	if i == 0 {
		// s.DownloadLimiter = rate.NewLimiter(rate.Inf, 0)
		s.DownloadLimiter.SetLimit(rate.Inf)
	} else {
		// s.DownloadLimiter = rate.NewLimiter(rate.Limit(i), 2<<18)
		s.DownloadLimiter.SetLimit(rate.Limit(i))
	}
}

func (s *BTService) SetUploadLimit(i int) {
	if i == 0 {
		// s.UploadLimiter = rate.NewLimiter(rate.Inf, 0)
		s.UploadLimiter.SetLimit(rate.Inf)
	} else {
		// s.UploadLimiter = rate.NewLimiter(rate.Limit(i), 2<<17)
		s.UploadLimiter.SetLimit(rate.Limit(i))
	}
}

func (s *BTService) RestoreLimits() {
	if s.config.MaxDownloadRate > 0 {
		s.SetDownloadLimit(s.config.MaxDownloadRate)
		s.log.Infof("Rate limiting download to %dkB/s", s.config.MaxDownloadRate/1024)
	} else {
		s.SetDownloadLimit(0)
	}

	if s.config.MaxUploadRate > 0 {
		s.SetUploadLimit(s.config.MaxUploadRate)
		s.log.Infof("Rate limiting upload to %dkB/s", s.config.MaxUploadRate/1024)
	} else {
		s.SetUploadLimit(0)
	}
}

func (s *BTService) SetBufferingLimits() {
	if s.config.LimitAfterBuffering {
		s.SetDownloadLimit(0)
		s.log.Info("Resetting rate limited download for buffering")
	}
}

func (s *BTService) GetSeedTime() int64 {
	return int64(s.config.SeedTimeLimit)
}

func (s *BTService) GetBufferSize() int64 {
	if s.config.BufferSize < endBufferSize {
		return endBufferSize
	} else {
		return s.config.BufferSize
	}
}

func (s *BTService) GetMemorySize() int64 {
	return s.config.MemorySize
}

func (s *BTService) GetStorageType() int {
	return s.config.DownloadStorage
}

func (s *BTService) PlayerStop() {
	log.Debugf("PlayerStop")

	// if s.config.DownloadStorage == estorage.StorageMemory {
	// 	s.DefaultStorage.Close()
	// }
}

func (s *BTService) PlayerSeek() {
	log.Debugf("PlayerSeek")

	// for _, t := range s.Torrents {
	// 	go t.SeekEvent()
	// }
}

func (s *BTService) ClientInfo(w io.Writer) {
	s.Client.WriteStatus(w)
}
