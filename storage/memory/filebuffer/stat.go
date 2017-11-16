package filebuffer

import (
	"os"
	"time"
)

type Stats struct {
	b *Buffer
	path string
}

// type FileInfo interface {
// 	Name() string       // base name of the file
// 	Size() int64        // length in bytes for regular files; system-dependent for others
// 	Mode() FileMode     // file mode bits
// 	ModTime() time.Time // modification time
// 	IsDir() bool        // abbreviation for Mode().IsDir()
// 	Sys() interface{}   // underlying data source (can return nil)
// }

func GetStats(path string, b *Buffer) *Stats {
	return &Stats{
		b: b,
		path: path,
	}
}

func (me *Stats) Name() string {
	return me.path
}

func (me *Stats) Size() int64 {
	return int64(me.b.Len())
}

func (me *Stats) Mode() os.FileMode {
	return 0755
}

func (me *Stats) ModTime() time.Time {
	return me.b.Modified
}

func (me *Stats) IsDir() bool {
	return false
}

func (me *Stats) Sys() interface{} {
	return nil
}
