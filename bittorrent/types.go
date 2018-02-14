package bittorrent

const (
	movieType   = "movie"
	showType    = "show"
	episodeType = "episode"
)

const (
	// StatusQueued ...
	StatusQueued = iota
	// StatusChecking ...
	StatusChecking
	// StatusFinding ...
	StatusFinding
	// StatusPaused ...
	StatusPaused
	// StatusBuffering ...
	StatusBuffering
	// StatusDownloading ...
	StatusDownloading
	// StatusFinished ...
	StatusFinished
	// StatusSeeding ...
	StatusSeeding
	// StatusAllocating ...
	StatusAllocating
	// StatusStalled ...
	StatusStalled
)

// StatusStrings ...
var StatusStrings = []string{
	"Queued",
	"Checking",
	"Finding",
	"Paused",
	"Buffering",
	"Downloading",
	"Finished",
	"Seeding",
	"Allocating",
	"Stalled",
}

const (
	// Remove ...
	Remove = iota
	// Active ...
	Active
)

// DefaultTrackers ...
var DefaultTrackers = []string{
	"udp://tracker.opentrackr.org:1337/announce",
	"udp://tracker.coppersurfer.tk:6969/announce",
	"udp://tracker.leechers-paradise.org:6969/announce",
	"udp://tracker.openbittorrent.com:80/announce",
	"udp://public.popcorn-tracker.org:6969/announce",
	"udp://explodie.org:6969",
}
