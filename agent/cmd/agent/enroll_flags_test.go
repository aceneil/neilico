package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"neilico/agent/internal/config"
	"neilico/agent/internal/state"
	"neilico/control-plane/pkg/enrolltoken"
)

// 已 enroll 过的节点，state.json 才是权威来源；令牌（一次性、会过期、可能被写成占位符）
// 不该让一个本来正常的 agent 起不来。
//
// 实测背景：compose 文件里留了个占位符令牌 → 容器 `Restarting (1)`，日志
//
//	neilico-agent: cannot read server from enrollment token; pass --server
//
// 而 state.json 里凭据、服务器地址一应俱全。同一条路径还有个更隐蔽的问题：
// state 里的 Server 从不被采纳，于是 cfg.Server 会停在 config.Default() 的占位地址
// （https://api.neilico.example.com），重启后 agent 打到错误服务器。
func TestApplyEnrollFlagsPrefersStateOverUnusableToken(t *testing.T) {
	const storedServer = "http://192.168.123.90:13000"

	writeState := func(t *testing.T) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "state.json")
		if err := state.Save(path, state.State{
			NodeID:     uuid.NewString(),
			AgentToken: "agent-token-from-first-enroll",
			PrivateKey: "private",
			PublicKey:  "public",
			Server:     storedServer,
		}); err != nil {
			t.Fatalf("save state: %v", err)
		}
		return path
	}
	newCfg := func() config.Config {
		cfg := config.Default()
		cfg.Token = ""     // run 路径不预置 agent token
		cfg.Node.Name = "" // 交给 applyEnrollFlags 用 hostname 填
		return cfg
	}

	// 具名包装：applyEnrollFlags 的参数是 (token, tokenFile, server, nodeName, statePath, stateDir)，
	// 位置参数太容易写错（我第一版就把 statePath 写进了 stateDir），这里固定好顺序。
	apply := func(cfg *config.Config, token, server, statePath string) error {
		return applyEnrollFlags(cfg, token, "", server, "", statePath, "")
	}

	t.Run("占位符令牌 + 有 state：不报错，服务器取自 state", func(t *testing.T) {
		cfg := newCfg()
		err := apply(&cfg, "粘贴控制面「接入设备」弹窗里的令牌", "", writeState(t))
		if err != nil {
			t.Fatalf("已 enroll 过的节点不该因为令牌不可解析而失败，实际: %v", err)
		}
		if cfg.Server != storedServer {
			t.Fatalf("server 应回落到 state 里的 %q，实际 %q", storedServer, cfg.Server)
		}
	})

	t.Run("完全不传令牌 + 有 state：服务器取自 state", func(t *testing.T) {
		cfg := newCfg()
		if err := apply(&cfg, "", "", writeState(t)); err != nil {
			t.Fatalf("不该失败: %v", err)
		}
		if cfg.Server != storedServer {
			t.Fatalf("server 应取自 state（%q），实际 %q", storedServer, cfg.Server)
		}
		if cfg.Server == config.Default().Server {
			t.Fatal("落在内置占位地址上会让 agent 打到错误服务器")
		}
	})

	t.Run("显式 --server 优先于 state", func(t *testing.T) {
		cfg := newCfg()
		if err := apply(&cfg, "", "http://explicit.example.com:9000", writeState(t)); err != nil {
			t.Fatalf("不该失败: %v", err)
		}
		if cfg.Server != "http://explicit.example.com:9000" {
			t.Fatalf("显式 --server 应优先，实际 %q", cfg.Server)
		}
	})

	t.Run("占位符令牌 + 无 state：必须明确报错（首次接入没有令牌就没法 enroll）", func(t *testing.T) {
		cfg := newCfg()
		err := apply(&cfg, "not-a-token", "", filepath.Join(t.TempDir(), "missing.json"))
		if err == nil {
			t.Fatal("无 state 且令牌不可解析时必须报错，否则会静默打到错误的服务器")
		}
	})

	t.Run("有效令牌 + 无 state：服务器取自令牌载荷", func(t *testing.T) {
		token, err := enrolltoken.Sign(enrolltoken.Payload{
			Version:   1,
			Server:    "http://cp.example.com:13000",
			TenantID:  uuid.New(),
			TokenID:   uuid.New(),
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
		}, []byte("test-key"))
		if err != nil {
			t.Fatalf("sign token: %v", err)
		}
		cfg := newCfg()
		if err := apply(&cfg, token, "", filepath.Join(t.TempDir(), "missing.json")); err != nil {
			t.Fatalf("令牌有效时不该失败: %v", err)
		}
		if cfg.Server != "http://cp.example.com:13000" {
			t.Fatalf("server 应取自令牌载荷，实际 %q", cfg.Server)
		}
	})

	t.Run("state 里的服务器为空：不被采纳，回落到原逻辑", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state.json")
		if err := state.Save(path, state.State{NodeID: uuid.NewString(), AgentToken: "t", Server: ""}); err != nil {
			t.Fatalf("save state: %v", err)
		}
		cfg := newCfg()
		if err := apply(&cfg, "not-a-token", "", path); err == nil {
			t.Fatal("state 没有可用服务器时仍应报错，而不是把空地址传下去")
		}
	})
}
