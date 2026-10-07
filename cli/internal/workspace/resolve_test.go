package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProjectBindingRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := SaveProjectBinding(dir, ProjectBinding{ProjectID: 42}); err != nil {
		t.Fatal(err)
	}
	b, err := LoadProjectBinding(dir)
	if err != nil || b == nil || b.ProjectID != 42 {
		t.Fatalf("binding = %+v, %v", b, err)
	}
	if err := RemoveProjectBinding(dir); err != nil {
		t.Fatal(err)
	}
	if b, _ := LoadProjectBinding(dir); b != nil {
		t.Fatal("删除后应读不到")
	}
	// 删不存在的绑定是成功。
	if err := RemoveProjectBinding(dir); err != nil {
		t.Fatal(err)
	}
}

// 向上查找会让「我在哪个项目里」变得不可预测。
func TestProjectBindingDoesNotSearchParentDirs(t *testing.T) {
	parent := t.TempDir()
	if err := SaveProjectBinding(parent, ProjectBinding{ProjectID: 42}); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(parent, "sub")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if b, err := LoadProjectBinding(child); err != nil || b != nil {
		t.Fatalf("子目录不该继承父目录绑定: %+v %v", b, err)
	}
}

func TestResolveProjectPriority(t *testing.T) {
	dir := t.TempDir()
	_ = SaveProjectBinding(dir, ProjectBinding{ProjectID: 42})

	if got, err := ResolveProject(7, dir); err != nil || got != 7 {
		t.Fatalf("--project 应优先: %d %v", got, err)
	}
	if got, err := ResolveProject(0, dir); err != nil || got != 42 {
		t.Fatalf("绑定应生效: %d %v", got, err)
	}
	// 没绑定时报错，不猜最近使用的项目——猜错会往别人的画布里写节点。
	if _, err := ResolveProject(0, t.TempDir()); err == nil {
		t.Fatal("无绑定应报错")
	}
}

func TestBindingIgnoresLegacyServerField(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".miaoju"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, ".miaoju", "project.json"), []byte(`{"version":1,"server":"http://x","projectId":42}`), 0o644)
	id, err := ResolveProject(0, dir)
	if err != nil || id != 42 {
		t.Fatalf("id=%d err=%v", id, err)
	}
}
