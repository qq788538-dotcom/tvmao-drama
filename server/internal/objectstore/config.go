// Package objectstore 把本机媒体上传到用户自己配置的对象存储（火山 TOS / 阿里云 OSS），
// 交给上游一个 24 小时有效的预签名地址。
package objectstore

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// SettingKey：配置整体以 JSON 存在 settings 表的这个键里。
const SettingKey = "object_storage"

const defaultPrefix = "miaoju-inputs"

type Config struct {
	Provider  string `json:"provider"`
	Endpoint  string `json:"endpoint"`
	Region    string `json:"region"`
	Bucket    string `json:"bucket"`
	Prefix    string `json:"prefix"`
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
}

// FieldError 标明哪个字段不合法，HTTP 层据此回 400 + field。
type FieldError struct{ Field, Msg string }

func (e *FieldError) Error() string { return e.Msg }

// Normalize 去空格、去 endpoint 的协议头和末尾斜杠、补默认前缀，并校验必填项。
func (c Config) Normalize() (Config, error) {
	trim := func(s string) string { return strings.TrimSpace(s) }
	c.Provider = strings.ToLower(trim(c.Provider))
	c.Endpoint = trim(c.Endpoint)
	c.Endpoint = strings.TrimPrefix(strings.TrimPrefix(c.Endpoint, "https://"), "http://")
	c.Endpoint = strings.TrimRight(c.Endpoint, "/")
	c.Region, c.Bucket, c.AccessKey, c.SecretKey = trim(c.Region), trim(c.Bucket), trim(c.AccessKey), trim(c.SecretKey)
	c.Prefix = strings.Trim(trim(c.Prefix), "/")
	if c.Prefix == "" {
		c.Prefix = defaultPrefix
	}
	if c.Provider != "tos" && c.Provider != "oss" {
		return c, &FieldError{"provider", "服务商只能是 TOS 或 OSS"}
	}
	fields := []struct{ name, label, v string }{
		{"endpoint", "Endpoint", c.Endpoint}, {"region", "Region", c.Region}, {"bucket", "Bucket", c.Bucket},
		{"prefix", "路径前缀", c.Prefix}, {"accessKey", "AccessKey", c.AccessKey}, {"secretKey", "SecretKey", c.SecretKey},
	}
	for _, f := range fields {
		if f.v == "" {
			return c, &FieldError{f.name, f.label + " 不能为空"}
		}
		if len(f.v) > 512 {
			return c, &FieldError{f.name, f.label + " 过长"}
		}
		if strings.IndexFunc(f.v, unicode.IsSpace) >= 0 {
			return c, &FieldError{f.name, f.label + " 不能包含空格"}
		}
	}
	return c, nil
}

type SettingsStore interface {
	Get(key string) (string, error)
	Set(key, value string) error
}

// Load 读配置；未保存过（或已清除）时 ok=false。
func Load(s SettingsStore) (Config, bool, error) {
	raw, err := s.Get(SettingKey)
	if err != nil || strings.TrimSpace(raw) == "" {
		return Config{}, false, err
	}
	var c Config
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return Config{}, false, fmt.Errorf("对象存储配置损坏: %w", err)
	}
	return c, true, nil
}

// Save 校验后整体写入。
func Save(s SettingsStore, c Config) error {
	n, err := c.Normalize()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(n)
	if err != nil {
		return err
	}
	return s.Set(SettingKey, string(raw))
}

func Clear(s SettingsStore) error { return s.Set(SettingKey, "") }
