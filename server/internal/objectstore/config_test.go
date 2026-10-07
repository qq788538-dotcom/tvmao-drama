package objectstore

import (
	"strings"
	"testing"
)

type memSettings map[string]string

func (m memSettings) Get(k string) (string, error) { return m[k], nil }
func (m memSettings) Set(k, v string) error        { m[k] = v; return nil }

func validCfg() Config {
	return Config{Provider: "tos", Endpoint: "tos-cn-beijing.volces.com", Region: "cn-beijing",
		Bucket: "b", AccessKey: "AK", SecretKey: "SK"}
}

func TestNormalizeConfig(t *testing.T) {
	c := validCfg()
	c.Endpoint = " https://tos-cn-beijing.volces.com/ "
	c.Prefix = "/a/b/"
	got, err := c.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if got.Endpoint != "tos-cn-beijing.volces.com" || got.Prefix != "a/b" {
		t.Fatalf("got %+v", got)
	}
	c = validCfg()
	c.Prefix = ""
	if got, _ := c.Normalize(); got.Prefix != "miaoju-inputs" {
		t.Fatalf("默认前缀 got %q", got.Prefix)
	}
}

func TestNormalizeRejects(t *testing.T) {
	cases := map[string]func(*Config){
		"provider":  func(c *Config) { c.Provider = "s3" },
		"endpoint":  func(c *Config) { c.Endpoint = "" },
		"region":    func(c *Config) { c.Region = "" },
		"bucket":    func(c *Config) { c.Bucket = "" },
		"accessKey": func(c *Config) { c.AccessKey = "" },
		"secretKey": func(c *Config) { c.SecretKey = "" },
		"space":     func(c *Config) { c.Bucket = "a b" },
		"long":      func(c *Config) { c.Bucket = strings.Repeat("a", 513) },
	}
	for name, mut := range cases {
		c := validCfg()
		mut(&c)
		if _, err := c.Normalize(); err == nil {
			t.Errorf("%s: 应报错", name)
		}
	}
}

func TestLoadSaveClear(t *testing.T) {
	s := memSettings{}
	if _, ok, err := Load(s); err != nil || ok {
		t.Fatalf("空库应未配置: ok=%v err=%v", ok, err)
	}
	if err := Save(s, validCfg()); err != nil {
		t.Fatal(err)
	}
	got, ok, err := Load(s)
	if err != nil || !ok || got.SecretKey != "SK" || got.Prefix != "miaoju-inputs" {
		t.Fatalf("got %+v ok=%v err=%v", got, ok, err)
	}
	if err := Clear(s); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := Load(s); ok {
		t.Fatal("清除后应未配置")
	}
}
