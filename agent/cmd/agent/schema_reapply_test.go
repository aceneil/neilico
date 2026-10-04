package main

import (
	"testing"

	"neilico/agent/internal/mesh"
)

// 升级 agent 后（state 里记录的本地应用逻辑版本落后）必须重新应用配置，
// 否则新增的本地动作（如对端路由）装不上——真机踩到过。
func TestNeedsSchemaReapply(t *testing.T) {
	if needsSchemaReapply(mesh.ApplicationSchemaVersion) {
		t.Fatal("与当前版本一致时不应重复应用")
	}
	if !needsSchemaReapply(mesh.ApplicationSchemaVersion - 1) {
		t.Fatal("版本落后时必须重新应用")
	}
	if !needsSchemaReapply(0) {
		t.Fatal("旧 state 没有该字段（0）时必须重新应用")
	}
	if !needsSchemaReapply(mesh.ApplicationSchemaVersion + 1) {
		t.Fatal("版本超前（降级 agent）时同样必须重新应用")
	}
}
