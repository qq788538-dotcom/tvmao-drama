package reveal

import "testing"

// explorer 自己解析 /select, 参数，不认 Go 的标准转义：只能给路径加引号，整段参数不能被包进引号。
func TestWindowsCmdLineQuotesOnlyThePath(t *testing.T) {
	got := windowsCmdLine(`C:\Users\张 三\AppData\Roaming\com.miaoju.desktop\media\u1\2026\10\a b.png`)
	want := `explorer.exe /select,"C:\Users\张 三\AppData\Roaming\com.miaoju.desktop\media\u1\2026\10\a b.png"`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}
