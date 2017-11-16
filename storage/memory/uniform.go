package memory

import (
	"io"
	"os"

	"github.com/anacrolix/missinggo/resource"
)

type uniformResourceProvider struct {
	*MemoryCache
}

var _ resource.Provider = &uniformResourceProvider{}

func (me *uniformResourceProvider) NewInstance(loc string) (resource.Instance, error) {
	return &uniformResource{me.MemoryCache, loc}, nil
}

type uniformResource struct {
	MemoryCache *MemoryCache
	Location    string
}

func (me *uniformResource) Get() (io.ReadCloser, error) {
	// log.Debugf("GET: %#v ", me.Location)
	return me.MemoryCache.OpenBuffer(me.Location)
}

func (me *uniformResource) Put(r io.Reader) (err error) {
	// log.Debugf("PUT: %#v --- %#v", me.Location, r)

	src := r.(*File)
	dst, err := me.MemoryCache.OpenBuffer(me.Location)
	if err != nil {
		return
	}
	defer dst.Close()

	// log.Debugf("PUT 1: %#v --- %#v", len(src.f.Chunks), len(dst.f.Chunks))
	dst.f.Chunks = src.f.Chunks
	// log.Debugf("PUT 2: %#v --- %#v", len(src.f.Chunks), len(dst.f.Chunks))

	return
}

func (me *uniformResource) ReadAt(b []byte, off int64) (n int, err error) {
	// log.Debugf("ReadAt: %#v --- %#v --- %#v", me.Location, len(b), off)
	mf, err := me.MemoryCache.OpenBuffer(me.Location)
	if err != nil {
		return
	}
	defer mf.Close()

	return mf.ReadAt(b, off)
}

func (me *uniformResource) WriteAt(b []byte, off int64) (n int, err error) {
	// log.Debugf("WriteAt: %#v --- %#v --- %#v", me.Location, len(b), off)
	mf, err := me.MemoryCache.OpenBuffer(me.Location)
	if err != nil {
		return
	}
	defer mf.Close()
	return mf.WriteAt(b, off)
}

func (me *uniformResource) Stat() (fi os.FileInfo, err error) {
	return me.MemoryCache.Stat(me.Location)
}

func (me *uniformResource) Delete() error {
	return me.MemoryCache.Remove(me.Location)
}
