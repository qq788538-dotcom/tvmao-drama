package canvasmanifest

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// 浏览器侧副本必须与真源**字节级**一致。
//
// 为什么是字节比对而不是逐字段比对：逐字段比对要先各写一份结构体，而结构体本身
// 就是漂移的入口（一侧加了字段、另一侧的解析忽略它，测试照样绿）。同一份 JSON
// 两处一份拷贝，字节不同就是不同，糊弄不过去。同 apps/flow-cli 的 scriptschema
// 纪律。
//
// 桌面仓是单仓：server/ 与 web/ 永远同时存在，找不到浏览器副本就是路径或文件被改坏了，
// 必须失败（spec A5：不一致则构建失败）。
func TestBrowserCopyInSync(t *testing.T) {
	const rel = "../../../../web/src/modules/canvas/editor/agent/canvasManifest.json"
	path, err := filepath.Abs(rel)
	if err != nil {
		t.Fatalf("解析浏览器副本路径失败: %v", err)
	}
	browser, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("找不到浏览器副本: %v", err)
	}
	if !bytes.Equal(browser, Raw()) {
		t.Fatalf("能力清单两侧不一致。真源 %d 字节，浏览器副本 %d 字节。\n"+
			"改清单必须两处一起改：\n"+
			"  cp server/internal/agent/canvasmanifest/manifest.json \\\n"+
			"     web/src/modules/canvas/editor/agent/canvasManifest.json",
			len(Raw()), len(browser))
	}
}
