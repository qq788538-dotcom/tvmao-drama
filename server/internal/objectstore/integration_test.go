package objectstore

import (
	"context"
	"os"
	"testing"
)

// 真实桶集成测试：设置 MIAOJU_TEST_<TOS|OSS>_{ENDPOINT,REGION,BUCKET,AK,SK} 后运行。
func TestRealProviders(t *testing.T) {
	for _, p := range []string{"TOS", "OSS"} {
		t.Run(p, func(t *testing.T) {
			env := func(k string) string { return os.Getenv("MIAOJU_TEST_" + p + "_" + k) }
			if env("BUCKET") == "" {
				t.Skip("未设置 MIAOJU_TEST_" + p + "_BUCKET")
			}
			c, err := Config{Provider: map[string]string{"TOS": "tos", "OSS": "oss"}[p],
				Endpoint: env("ENDPOINT"), Region: env("REGION"), Bucket: env("BUCKET"),
				AccessKey: env("AK"), SecretKey: env("SK"), Prefix: "miaoju-it"}.Normalize()
			if err != nil {
				t.Fatal(err)
			}
			cl, err := NewClient(c)
			if err != nil {
				t.Fatal(err)
			}
			if r := Probe(context.Background(), cl, c.Prefix, HTTPGet); !r.OK {
				t.Fatalf("%+v", r)
			}
		})
	}
}

func TestProvidersRegistered(t *testing.T) {
	for _, p := range []string{"tos", "oss"} {
		if _, ok := providers[p]; !ok {
			t.Errorf("%s 未登记", p)
		}
	}
}
