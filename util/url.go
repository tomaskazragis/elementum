package util

import (
	"fmt"
	"strings"
)

// TrailerURL returns trailer url, constructed for Kodi
func TrailerURL(u string) (ret string) {
	if len(u) == 0 {
		return
	}

	if strings.Contains(u, "?v=") {
		ret = fmt.Sprintf("plugin://plugin.video.youtube/play/?video_id=%s", strings.Split(u, "?v=")[1])
	} else {
		ret = fmt.Sprintf("plugin://plugin.video.youtube/play/?video_id=%s", u)
	}

	return
}
