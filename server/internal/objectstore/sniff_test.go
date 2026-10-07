package objectstore

import "testing"

func TestSniffMedia(t *testing.T) {
	pad := make([]byte, 32)
	cat := func(b ...[]byte) []byte {
		var o []byte
		for _, x := range b {
			o = append(o, x...)
		}
		return o
	}
	cases := []struct {
		name       string
		head       []byte
		ext, ctype string
	}{
		{"mp4", cat([]byte{0, 0, 0, 0x18}, []byte("ftypisom"), pad), ".mp4", "video/mp4"},
		{"mov", cat([]byte{0, 0, 0, 0x14}, []byte("ftypqt  "), pad), ".mov", "video/quicktime"},
		{"m4a", cat([]byte{0, 0, 0, 0x18}, []byte("ftypM4A "), pad), ".m4a", "audio/mp4"},
		{"webm", cat([]byte{0x1A, 0x45, 0xDF, 0xA3}, pad), ".webm", "video/webm"},
		{"mp3 id3", cat([]byte("ID3"), pad), ".mp3", "audio/mpeg"},
		{"mp3 sync", cat([]byte{0xFF, 0xFB, 0x90}, pad), ".mp3", "audio/mpeg"},
		{"wav", cat([]byte("RIFF"), []byte{1, 2, 3, 4}, []byte("WAVEfmt "), pad), ".wav", "audio/wav"},
		{"ogg", cat([]byte("OggS"), pad), ".ogg", "audio/ogg"},
		{"png", cat([]byte("\x89PNG\r\n\x1a\n"), pad), ".png", "image/png"},
		{"jpeg", cat([]byte{0xFF, 0xD8, 0xFF, 0xE0}, pad), ".jpg", "image/jpeg"},
		{"gif", cat([]byte("GIF89a"), pad), ".gif", "image/gif"},
		{"webp", cat([]byte("RIFF"), []byte{1, 2, 3, 4}, []byte("WEBPVP8 "), pad), ".webp", "image/webp"},
		{"unknown", []byte("hello world, plain text"), "", ""},
		{"empty", nil, "", ""},
		{"short", []byte{0xFF}, "", ""},
	}
	for _, c := range cases {
		ext, ct := sniffMedia(c.head)
		if ext != c.ext || ct != c.ctype {
			t.Errorf("%s: got (%q,%q) want (%q,%q)", c.name, ext, ct, c.ext, c.ctype)
		}
	}
}
