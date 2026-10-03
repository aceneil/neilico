package db

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"neilico/control-plane/internal/models"
	pkgcapabilities "neilico/control-plane/pkg/capabilities"
)

// 这类缺陷只有「在已有数据的表上加新列」才暴露：
// 全新空库跑 AutoMigrate 永远成功，所以普通单测抓不到。
// 真实事故：nodes 表新增 capabilities 时 tag 写成 `not null` 而无 default
//
//	→ AutoMigrate 生成 `ADD COLUMN capabilities JSONB NOT NULL`（无默认值）
//	→ PostgreSQL 报 "column \"capabilities\" of relation \"nodes\" contains null values"
//	→ 容器启动即崩、无限重启（升级已有部署必现）。
//
// 这里用「当前模型建表 → 删掉新列 → 用原始 SQL 插入历史行 → 再 AutoMigrate」，
// 精确复现真实升级路径。SQLite 与 PostgreSQL 对 “ADD NOT NULL 无默认值” 都会拒绝，
// 所以该测试在 sqlite 上同样有判别力。
func TestAutoMigrateAddsNewNotNullColumnToPopulatedTable(t *testing.T) {
	handle, err := gorm.Open(sqlite.Open("file:upgrade_guard?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	// 1) 按当前模型建表
	if err := handle.AutoMigrate(&models.Node{}); err != nil {
		t.Fatalf("初次 AutoMigrate: %v", err)
	}
	// 2) 删掉新列 → 等价于升级前的旧表结构
	if err := handle.Migrator().DropColumn(&models.Node{}, "capabilities"); err != nil {
		t.Fatalf("构造旧表（删除 capabilities 列）失败：%v", err)
	}
	// 3) 用原始 SQL 插入一行历史数据（不能走模型：模型已含 capabilities 列）
	legacyInsert := `INSERT INTO nodes
        (id, tenant_id, name, public_key, private_key, os, arch, version, status, tags, agent_token_hash, created_at)
        VALUES ('11111111-1111-1111-1111-111111111111',
                '22222222-2222-2222-2222-222222222222',
                'existing-node', 'PUBKEY', '', 'linux', 'amd64', '0.1.0', 'offline', '[]', 'deadbeef',
                CURRENT_TIMESTAMP)`
	if err := handle.Exec(legacyInsert).Error; err != nil {
		t.Fatalf("写入历史行失败：%v", err)
	}

	// 4) 升级路径：新增 NOT NULL 列必须带 default，否则这里就炸
	if err := handle.AutoMigrate(&models.Node{}); err != nil {
		t.Fatalf("AutoMigrate 在已有数据的表上失败（新增 NOT NULL 列缺少 default，真实部署会崩溃重启）：%v", err)
	}

	// 5) 历史行必须仍可读，且读取边界能把空默认值补成明确状态
	var node models.Node
	if err := handle.Where("name = ?", "existing-node").First(&node).Error; err != nil {
		t.Fatalf("升级后读取历史行失败：%v", err)
	}
	caps := node.Capabilities.Data().Normalize()
	if caps.Mesh != pkgcapabilities.MeshUnavailable || caps.Reason == "" {
		t.Fatalf("历史行的 capabilities 规范化后仍不可用：%+v", caps)
	}
}
