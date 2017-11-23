package memory

import (
	"os"
	"time"
)

type Stats struct {
	path     string
	size     int64
	modified time.Time
}

// type FileInfo interface {
// 	Name() string       // base name of the file
// 	Size() int64        // length in bytes for regular files; system-dependent for others
// 	Mode() FileMode     // file mode bits
// 	ModTime() time.Time // modification time
// 	IsDir() bool        // abbreviation for Mode().IsDir()
// 	Sys() interface{}   // underlying data source (can return nil)
// }

func (me *Stats) Name() string {
	return me.path
}

func (me *Stats) Size() int64 {
	return me.size
}

func (me *Stats) Mode() os.FileMode {
	return 0755
}

func (me *Stats) ModTime() time.Time {
	return me.modified
}

func (me *Stats) IsDir() bool {
	return false
}

func (me *Stats) Sys() interface{} {
	return nil
}
