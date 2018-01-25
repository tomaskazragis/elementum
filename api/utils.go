package api

import (
	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/xbmc"
)

// type contextMenu []*contextMenuItem
//
// type contextMenuItem []string
//
// // contextMenuRequest ...
// type contextMenuRequest struct {
// }
//
// func makeContextMenu(r contextMenuRequest) *contextMenu {
// 	m := &contextMenu{}
//
// }

func filterListItems(l xbmc.ListItems) xbmc.ListItems {
	t := config.Get().TraktToken != ""

	ret := make(xbmc.ListItems, 0)
	for _, i := range l {
		if !i.TraktAuth || t {
			ret = append(ret, i)
		}
	}

	return ret
}
