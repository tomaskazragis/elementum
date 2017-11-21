package main

import (
	"net/http"
	"os"
	"time"
	// _ "net/http/pprof"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/elgatito/elementum/api"
	"github.com/elgatito/elementum/bittorrent"
	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/database"
	"github.com/elgatito/elementum/lockfile"
	"github.com/elgatito/elementum/trakt"
	"github.com/elgatito/elementum/util"
	"github.com/elgatito/elementum/xbmc"
	"github.com/op/go-logging"
)

var log = logging.MustGetLogger("main")

func ensureSingleInstance(conf *config.Configuration) (lock *lockfile.LockFile, err error) {
	file := filepath.Join(conf.Info.Path, ".lockfile")
	lock, err = lockfile.New(file)
	if err != nil {
		log.Critical("Unable to initialize lockfile:", err)
		return
	}
	var pid int
	var p *os.Process
	pid, err = lock.Lock()
	if err != nil {
		log.Warningf("Unable to acquire lock %q: %v, killing...", lock.File, err)
		p, err = os.FindProcess(pid)
		if err != nil {
			log.Warning("Unable to find other process:", err)
			return
		}
		if err = p.Kill(); err != nil {
			log.Critical("Unable to kill other process:", err)
			return
		}
		if err = os.Remove(lock.File); err != nil {
			log.Critical("Unable to remove lockfile")
			return
		}
		_, err = lock.Lock()
	}
	return
}

func makeBTConfiguration(conf *config.Configuration) *bittorrent.BTConfiguration {
	btConfig := &bittorrent.BTConfiguration{
		DownloadStorage:     conf.DownloadStorage,
		MemorySize:          int64(conf.MemorySize),
		BufferSize:          int64(conf.BufferSize),
		SpoofUserAgent:      conf.SpoofUserAgent,
		MaxUploadRate:       conf.UploadRateLimit,
		MaxDownloadRate:     conf.DownloadRateLimit,
		LimitAfterBuffering: conf.LimitAfterBuffering,
		ConnectionsLimit:    conf.ConnectionsLimit,
		// SessionSave:         conf.SessionSave,
		// ShareRatioLimit:     conf.ShareRatioLimit,
		// SeedTimeRatioLimit:  conf.SeedTimeRatioLimit,
		SeedTimeLimit: conf.SeedTimeLimit,
		DisableDHT:    conf.DisableDHT,
		// DisableUPNP:         conf.DisableUPNP,
		EncryptionPolicy:   conf.EncryptionPolicy,
		LowerListenPort:    conf.BTListenPortMin,
		UpperListenPort:    conf.BTListenPortMax,
		ListenInterfaces:   conf.ListenInterfaces,
		OutgoingInterfaces: conf.OutgoingInterfaces,
		// TunedStorage:        conf.TunedStorage,
		DownloadPath:        conf.DownloadPath,
		TorrentsPath:        conf.TorrentsPath,
		DisableBgProgress:   conf.DisableBgProgress,
		CompletedMove:       conf.CompletedMove,
		CompletedMoviesPath: conf.CompletedMoviesPath,
		CompletedShowsPath:  conf.CompletedShowsPath,
	}

	// if conf.SocksEnabled == true {
	// 	btConfig.Proxy = &bittorrent.ProxySettings{
	// 		Type:     conf.ProxyType,
	// 		Hostname: conf.SocksHost,
	// 		Port:     conf.SocksPort,
	// 		Username: conf.SocksLogin,
	// 		Password: conf.SocksPassword,
	// 	}
	// }

	return btConfig
}

func main() {
	// Make sure we are properly multithreaded.
	runtime.GOMAXPROCS(runtime.NumCPU())

	logging.SetFormatter(logging.MustStringFormatter(
		`%{color}%{level:.4s}  %{module:-12s} ▶ %{shortfunc:-15s}  %{color:reset}%{message}`,
	))
	logging.SetBackend(logging.NewLogBackend(os.Stdout, "", 0))

	log.Infof("Starting Elementum daemon")
	log.Infof("Version: %s Go: %s", util.Version[1:len(util.Version)-1], runtime.Version())

	conf := config.Reload()

	log.Infof("Addon: %s v%s", conf.Info.Id, conf.Info.Version)

	lock, err := ensureSingleInstance(conf)
	defer lock.Unlock()
	if err != nil {
		log.Warningf("Unable to acquire lock %q: %v, exiting...", lock.File, err)
		os.Exit(1)
	}

	wasFirstRun := Migrate()

	db, err := database.InitDB(conf)
	if err != nil {
		log.Error(err)
		return
	}

	api.InitDB()
	bittorrent.InitDB()

	btService := bittorrent.NewBTService(*makeBTConfiguration(conf))

	var shutdown = func() {
		log.Info("Shutting down...")
		api.CloseLibrary()
		btService.Close()
		db.Close()
		log.Info("Goodbye")
		os.Exit(0)
	}

	var watchParentProcess = func() {
		for {
			if os.Getppid() == 1 {
				log.Warning("Parent shut down, shutting down too...")
				go shutdown()
				break
			}
			time.Sleep(1 * time.Second)
		}
	}
	go watchParentProcess()

	http.Handle("/", api.Routes(btService))
	http.Handle("/files/", bittorrent.TorrentFSHandler(btService, config.Get().DownloadPath))
	http.Handle("/reload", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		btService.Reconfigure(*makeBTConfiguration(config.Reload()))
	}))
	http.Handle("/shutdown", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		shutdown()
	}))

	xbmc.Notify("Elementum", "LOCALIZE[30208]", config.AddonIcon())

	go func() {
		if !wasFirstRun {
			log.Info("Updating Kodi add-on repositories...")
			xbmc.UpdateAddonRepos()
		}

		xbmc.ResetRPC()
	}()

	go api.LibraryUpdate()
	go api.LibraryListener()
	go trakt.TokenRefreshHandler()

	s := &http.Server{
		Addr:         ":" + strconv.Itoa(config.ListenPort),
		Handler:      nil,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,

		MaxHeaderBytes: 1 << 20,
	}
	s.ListenAndServe()
}
