package canvasscript

import (
	"context"
	"testing"
)

func runOneTask(t *testing.T, svc *Service, sk *fakeSkills) {
	t.Helper()
	submitOne(t, svc)
	task, ok := svc.claimOne(context.Background())
	if !ok {
		t.Fatal("应抢到任务")
	}
	svc.execute(context.Background(), task, testConfig())
	if len(sk.runs) != 1 {
		t.Fatalf("应调一次上游, got %d", len(sk.runs))
	}
}

// 剧本生成分镜：canvas_script_config 选中的模型配了独立上游，就用模型自己的地址和 Key。
func TestExecuteUsesModelOwnUpstream(t *testing.T) {
	sk := &fakeSkills{content: validCandidate}
	svc, _ := newTestService(t, testConfig(), sk, fakeModels{upstream: map[string][2]string{testModel: {"https://own.example", "sk-own"}}})
	runOneTask(t, svc, sk)
	if m := sk.runs[0].Model; m.BaseURL != "https://own.example" || m.APIKey != "sk-own" {
		t.Fatalf("应走模型独立上游: %#v", m)
	}
}

func TestExecuteFallsBackToGlobalUpstream(t *testing.T) {
	sk := &fakeSkills{content: validCandidate}
	svc, _ := newTestService(t, testConfig(), sk, fakeModels{})
	runOneTask(t, svc, sk)
	if m := sk.runs[0].Model; m.BaseURL != "https://up.example" || m.APIKey != "sk-test" {
		t.Fatalf("应走全局上游: %#v", m)
	}
}

// 全局地址没配，但模型配了独立上游：不应以“未配置上游地址”失败。
func TestExecuteModelOwnUpstreamWithoutGlobal(t *testing.T) {
	sk := &fakeSkills{content: validCandidate}
	svc, conn := newTestService(t, testConfig(), sk, fakeModels{upstream: map[string][2]string{testModel: {"https://own.example", "sk-own"}}})
	_ = svc.settings.Set("base_domain", "")
	id := submitOne(t, svc)
	task, _ := svc.claimOne(context.Background())
	svc.execute(context.Background(), task, testConfig())
	if st, msg, _ := taskRow(t, conn, id); st != StatusSucceeded {
		t.Fatalf("status=%s err=%s", st, msg)
	}
}
