package generation

import (
	"reflect"
	"testing"
)

func TestFilterHTTPURLs(t *testing.T) {
	in := []string{
		"https://up/a.mp4",
		"http://up/b.mp4",
		"data:video/mp4;base64,AAAA",
		"",
		"media://k",
	}
	got := filterHTTPURLs(in)
	want := []string{"https://up/a.mp4", "http://up/b.mp4"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterHTTPURLs = %v, want %v", got, want)
	}
}
