package directormodels

import (
	"errors"
	"strings"
	"testing"
)

// fakeRepo 是 Repository 的手写 fake，只实现 CanCreate 用到的 CountByUser。
type fakeRepo struct {
	Repository // 其余方法未实现，误调直接 panic——测试只该走 CountByUser
	count      int64
	err        error
}

func (f fakeRepo) CountByUser(int64) (int64, error) { return f.count, f.err }

func TestCanCreate(t *testing.T) {
	cases := []struct {
		name   string
		count  int64
		wantOK bool
	}{
		{"上限内放行", UserModelLimit - 1, true},
		{"达上限即拒", UserModelLimit, false},
		// 已超上限（历史数据超发时）也必须拒，不只是恰好等于。
		{"超上限拒绝", UserModelLimit + 1, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := &Service{Repo: fakeRepo{count: c.count}}
			limit, ok, err := svc.CanCreate(1)
			if err != nil {
				t.Fatalf("CanCreate err: %v", err)
			}
			// limit 是 409 响应体要展示的值，必须始终等于常量。
			if limit != UserModelLimit || ok != c.wantOK {
				t.Fatalf("CanCreate=(%d,%v), want (%d,%v)", limit, ok, UserModelLimit, c.wantOK)
			}
		})
	}
}

func TestCanCreate_RepoError(t *testing.T) {
	svc := &Service{Repo: fakeRepo{err: errors.New("boom")}}
	if _, ok, err := svc.CanCreate(1); err == nil || ok {
		t.Fatalf("repo 出错必须 (ok=false, err!=nil)，得到 ok=%v err=%v", ok, err)
	}
}

func TestValidateUpload(t *testing.T) {
	cases := []struct {
		name     string
		filename string
		size     int64
		wantErr  error
	}{
		{"fbx 放行", "robot.fbx", 1024, nil},
		{"obj 放行", "scene.obj", 1024, nil},
		// GLB 是 glTF 的单文件二进制封装，单文件上传即完整，放行。
		{"glb 放行", "robot.glb", 1024, nil},
		// 扩展名不区分大小写——用户从建模软件导出常是全大写。
		{"大写扩展名放行", "ROBOT.FBX", 1024, nil},
		{"混合大小写放行", "Scene.Obj", 1024, nil},
		{"大写 GLB 放行", "PROP.GLB", 1024, nil},
		// .gltf 是多文件格式（JSON 主文件外挂 .bin 与贴图），单文件上传必缺
		// 贴图——必须拒绝，不能因为「都是 glTF 家族」跟着 .glb 一起放行。
		{"gltf 拒绝", "robot.gltf", 1024, ErrUnsupportedExt},
		{"无扩展名拒绝", "robot", 1024, ErrUnsupportedExt},
		// 恰好 30MB 是合法边界（契约是「>30MB → 400」）。
		{"恰好 30MB 放行", "big.fbx", MaxFileSizeBytes, nil},
		{"超 30MB 拒绝", "big.fbx", MaxFileSizeBytes + 1, ErrFileTooLarge},
		// 扩展名校验在大小之前：两个都错时报扩展名（更能指向根因）。
		{"扩展名错误优先报", "big.gltf", MaxFileSizeBytes + 1, ErrUnsupportedExt},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateUpload(c.filename, c.size)
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("ValidateUpload(%q,%d)=%v, want %v", c.filename, c.size, err, c.wantErr)
			}
		})
	}
}

func TestReferenceLikePattern(t *testing.T) {
	// 引用检查扫的是快照 JSON 里的 media://<key> 字符串；LIKE 通配符必须转义，
	// 否则 key 里的下划线（uniqueUploadFilename 必带）会当单字符通配用。
	cases := []struct {
		key  string
		want string
	}{
		{"tapnow/u1/2026/08/ab12_robot.fbx", `%media://tapnow/u1/2026/08/ab12\_robot.fbx%`},
		{"a%b.obj", `%media://a\%b.obj%`},
		{`a\b.obj`, `%media://a\\b.obj%`},
	}
	for _, c := range cases {
		if got := ReferenceLikePattern(c.key); got != c.want {
			t.Fatalf("ReferenceLikePattern(%q)=%q, want %q", c.key, got, c.want)
		}
	}
	// 前后必须是 % 包裹的子串匹配，中间必须带 media:// 前缀——
	// 少了前缀会把裸 key 出现在别处（比如日志字段）也算成引用。
	if p := ReferenceLikePattern("k.fbx"); !strings.HasPrefix(p, "%media://") || !strings.HasSuffix(p, "%") {
		t.Fatalf("pattern 形状不对: %q", p)
	}
}
