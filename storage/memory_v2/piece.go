package memory

import (
	"errors"
	"os"
	"sync"
	"time"

	"github.com/RoaringBitmap/roaring"
	"github.com/anacrolix/torrent/storage"
)

// Piece stores meta information about buffer contents
type Piece struct {
	c *Cache

	Index     int
	Length    int64
	Position  int
	Hash      string
	Active    bool
	Completed bool
	Size      int64
	Modified  time.Time

	Chunks *roaring.Bitmap
	mu     sync.Mutex

	// Path      string
	// Index     int64
	// Position  int
	// Active    bool
	// Completed bool
	// Size      int64
	// Accessed  time.Time
	// Chunks    *roaring.Bitmap
	// mu        sync.Mutex
}

func (p *Piece) Stat() (os.FileInfo, error) {
	if !p.Active {
		return nil, errors.New("Not ready")
	}

	return &Stats{
		path:     p.Hash,
		size:     p.Size,
		modified: p.Modified,
	}, nil
}

func (p *Piece) Completion() storage.Completion {
	fi, err := p.Stat()
	return storage.Completion{
		Complete: err == nil && fi.Size() == p.Length && p.Length != 0 && p.Completed,
		Ok:       true,
	}
}

func (p *Piece) MarkComplete() error {
	// return resource.Move(s.i, s.c)
	p.Completed = true
	return nil
}

func (p *Piece) MarkNotComplete() error {
	p.Completed = false
	return nil
}

func (p *Piece) ReadAt(b []byte, off int64) (int, error) {
	buf, err := p.OpenBuffer(false)
	if err != nil {
		return 0, nil
	}

	return buf.ReadAt(b, off)

	// log.Debugf("ReadAt: %#v, %#v, %#v", p.Index, off, len(b))
	// return p.c.ReadAt(p.Index, b, off)
	// if p.Completion().Complete {
	// 	return p.c.ReadAt(b, off)
	// } else {
	// 	return p.c.ReadAt(b, off)
	// }
}

func (p *Piece) WriteAt(b []byte, off int64) (n int, err error) {
	buf, err := p.OpenBuffer(true)
	if err != nil {
		return 0, err
	}

	return buf.WriteAt(b, off)

	// log.Debugf("WriteAt: %#v, %#v, %#v", p.Index, off, len(b))
	// p.c.mu.Lock()
	// defer p.c.mu.Unlock()
	// p.OpenBuffer(true)
	// log.Debugf("WriteAt2: %#v, %#v", p.pi.Index, len(p.c.pieces))
	// return p.c.WriteAt(p.Index, b, off)

	// buf, err := p.OpenBuffer(true)
	// if err != nil {
	// 	return 0, err
	// }
	//
	// return buf.WriteAt(b, off)

}

func (p *Piece) OpenBuffer(iswrite bool) (ret *Buffer, err error) {
	p.c.mu.Lock()
	defer p.c.mu.Unlock()

	if p.Index >= len(p.c.pieces) {
		return nil, errors.New("Piece index not valid")
	}

	if !p.c.pieces[p.Index].Active && iswrite {
		for index, v := range p.c.positions {
			if v.Used {
				continue
			}

			v.Used = true
			v.Index = p.Index

			selected := p.c.pieces[p.Index]

			selected.Position = index
			selected.Active = true
			selected.Size = 0
			selected.Modified = time.Now()

			break
		}

		if !p.c.pieces[p.Index].Active {
			log.Debugf("Buffer not assigned: %#v", p.c.positions)
			return nil, errors.New("Could not assign buffer")
		}
	}

	ret = &Buffer{
		c: p.c,
		p: p,
	}

	return
}
