package main

import "testing"

// 装配必须构造对象存储 Publisher 并接入设置接口（对象存储设计 §3.2）。
func TestWireInjectsObjectStorePublisher(t *testing.T) {
	e := newE2E(t)
	a := e.app
	if a.publisher == nil {
		t.Fatal("wire 应构造对象存储 Publisher")
	}
	if a.deps.SettingsCenter.ObjectStore == nil {
		t.Fatal("设置接口应拿到 ObjectStore（连接测试）")
	}
	if a.publisher.Configured() {
		t.Fatal("新库默认未配置")
	}
}
