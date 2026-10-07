package objectstore

import (
	"bytes"
	"net/http"
)

// sniffMedia 按文件头魔数识别常见媒体容器，返回扩展名（含点）与 Content-Type；
// 识别不了返回空串。http.DetectContentType 对 mp4/m4a/mp3 等覆盖不全，故先自行判断。
func sniffMedia(head []byte) (ext, ctype string) {
	switch {
	case len(head) >= 12 && bytes.Equal(head[4:8], []byte("ftyp")):
		switch string(head[8:12]) {
		case "qt  ":
			return ".mov", "video/quicktime"
		case "M4A ":
			return ".m4a", "audio/mp4"
		}
		return ".mp4", "video/mp4"
	case bytes.HasPrefix(head, []byte{0x1A, 0x45, 0xDF, 0xA3}):
		return ".webm", "video/webm"
	case bytes.HasPrefix(head, []byte("ID3")),
		len(head) >= 2 && head[0] == 0xFF && head[1]&0xE0 == 0xE0:
		return ".mp3", "audio/mpeg"
	case len(head) >= 12 && bytes.Equal(head[:4], []byte("RIFF")) && bytes.Equal(head[8:12], []byte("WAVE")):
		return ".wav", "audio/wav"
	case bytes.HasPrefix(head, []byte("OggS")):
		return ".ogg", "audio/ogg"
	}
	// 图片交给标准库识别
	switch ct := http.DetectContentType(head); ct {
	case "image/png":
		return ".png", ct
	case "image/jpeg":
		return ".jpg", ct
	case "image/gif":
		return ".gif", ct
	case "image/webp":
		return ".webp", ct
	}
	return "", ""
}
