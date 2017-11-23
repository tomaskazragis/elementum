package cache

import (
	"io"
	"os"

	"github.com/anacrolix/missinggo/resource"
)

type uniformResourceProvider struct {
	*Cache
}

var _ resource.Provider = &uniformResourceProvider{}

func (me *uniformResourceProvider) NewInstance(loc string) (resource.Instance, error) {
	return &uniformResource{me.Cache, loc}, nil
}

type uniformResource struct {
	Cache    *Cache
	Location string
}

func (me *uniformResource) Get() (io.ReadCloser, error) {
	log.Debugf("GET: %#v ", me.Location)
	return me.Cache.OpenBuffer(me.Location, false)
}

func (me *uniformResource) Put(r io.Reader) (err error) {
	log.Debugf("PUT: %#v --- %#v", me.Location, r)

	src := r.(*File)
	src.c.pieces[src.key].Path = me.Location
	src.c.pieces[src.key].Completed = true

	// dst, err := me.Cache.OpenBuffer(me.Location, true)
	// if err != nil {
	// 	return
	// }

	// dst.c.pieces[dst.key].Position = dst.c.pieces[src.key].Position
	// dst.c.pieces[dst.key].Size = dst.c.pieces[src.key].Size
	// dst.c.pieces[dst.key].Accessed = dst.c.pieces[src.key].Accessed
	// dst.c.pieces[dst.key].Active = dst.c.pieces[src.key].Active
	//
	// // delete(dst.c.pieces, src.key)
	// dst.c.pieces[src.key].Position = -1
	// dst.c.pieces[src.key].Active = false
	// // dst.c.pieces[src.key].Size = 0

	// src := r.(*File)
	// dst, err := me.Cache.OpenBuffer(me.Location)
	// if err != nil {
	// 	return
	// }
	// defer dst.Close()

	// log.Debugf("PUT 1: %#v --- %#v", len(src.f.Chunks), len(dst.f.Chunks))
	// dst.f.Chunks = src.f.Chunks
	// log.Debugf("PUT 2: %#v --- %#v", len(src.f.Chunks), len(dst.f.Chunks))

	return
}

func (me *uniformResource) ReadAt(b []byte, off int64) (n int, err error) {
	log.Debugf("ReadAt: %#v --- %#v --- %#v", me.Location, len(b), off)
	f, err := me.Cache.OpenBuffer(me.Location, false)
	if err != nil {
		return
	}
	return f.ReadAt(b, off)
}

func (me *uniformResource) WriteAt(b []byte, off int64) (n int, err error) {
	// log.Debugf("WriteAt: %#v --- %#v --- %#v", me.Location, len(b), off)
	f, err := me.Cache.OpenBuffer(me.Location, true)
	if err != nil {
		return
	}
	return f.WriteAt(b, off)
}

func (me *uniformResource) Stat() (fi os.FileInfo, err error) {
	// log.Debugf("Stat: %#v", me.Location)
	return me.Cache.Stat(me.Location)
	// fi, err = me.Cache.Stat(me.Location)
	// log.Debugf("Stat: %#v, %#v, %#v", me.Location, fi, err)
	// return
	// return me.MemoryCache.Stat(me.Location)
}

func (me *uniformResource) Delete() error {
	log.Debugf("DELETE: %#v", me.Location)
	return me.Cache.Remove(me.Location)
}
