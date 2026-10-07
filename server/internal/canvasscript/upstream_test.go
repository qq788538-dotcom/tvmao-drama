package canvasscript

import (
	"context"
	"errors"
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

// M-1：独立上游地址存成了 .../v1（旧值），交给 skillruntime 的是根地址，由它补一层 /v1。
func TestExecuteOwnUpstreamWithTrailingV1(t *testing.T) {
	sk := &fakeSkills{content: validCandidate}
	svc, _ := newTestService(t, testConfig(), sk, fakeModels{upstream: map[string][2]string{testModel: {"https://own.example/v1", "sk-own"}}})
	runOneTask(t, svc, sk)
	if m := sk.runs[0].Model; m.BaseURL != "https://own.example" {
		t.Fatalf("应交出规范化后的根地址: %#v", m)
	}
}

// M-5：查模型行遇到 ErrNotFound 以外的错误（数据库忙等）时不能静默回落全局——那可能是另一个
// 计费账号，剧本内容也会发过去。应失败（按既有逻辑重试），不调上游。
func TestExecuteModelLookupErrorDoesNotFallBackToGlobal(t *testing.T) {
	sk := &fakeSkills{content: validCandidate}
	svc, conn := newTestService(t, testConfig(), sk, fakeModels{})
	id := submitOne(t, svc)
	svc.models = fakeModels{err: errors.New("database is locked")}
	task, _ := svc.claimOne(context.Background())
	svc.execute(context.Background(), task, testConfig())
	if len(sk.runs) != 0 {
		t.Fatalf("不应调上游: %d", len(sk.runs))
	}
	if st, _, _ := taskRow(t, conn, id); st == StatusSucceeded {
		t.Fatalf("status=%s", st)
	}
}
