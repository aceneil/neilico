package db

import (
	"fmt"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"umpp/control-plane/internal/config"
	"umpp/control-plane/internal/models"
)

func Open(cfg config.Database, logLevels ...string) (*gorm.DB, error) {
	logLevel := "error"
	if len(logLevels) > 0 {
		logLevel = logLevels[0]
	}
	gormConfig := &gorm.Config{
		Logger: logger.Default.LogMode(parseLogLevel(logLevel)),
	}
	var dialector gorm.Dialector
	switch cfg.Driver {
	case "postgres":
		dialector = postgres.Open(cfg.DSN)
	case "sqlite":
		dialector = sqlite.Open(cfg.DSN)
	default:
		return nil, fmt.Errorf("unsupported database driver %q", cfg.Driver)
	}
	handle, err := gorm.Open(dialector, gormConfig)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	sqlDB, err := handle.DB()
	if err != nil {
		return nil, fmt.Errorf("get database handle: %w", err)
	}
	if cfg.Driver == "sqlite" {
		sqlDB.SetMaxOpenConns(1)
	}
	return handle, nil
}

func AutoMigrate(handle *gorm.DB) error {
	if err := handle.AutoMigrate(
		&models.Tenant{},
		&models.User{},
		&models.Certificate{},
		&models.Domain{},
		&models.ProxyRule{},
		&models.Node{},
		&models.VirtualNetwork{},
		&models.NetworkMember{},
		&models.ACLRule{},
		&models.SubnetRoute{},
		&models.RelayServer{},
		&models.ConfigVersion{},
		&models.AuditLog{},
		&models.TrafficLog{},
	); err != nil {
		return fmt.Errorf("automigrate: %w", err)
	}
	// Older M2b deployments created a global virtual-IP unique index. Network
	// virtual IPs only need to be unique within one network; normalize both
	// fresh and existing databases before serving traffic.
	if err := handle.Exec("DROP INDEX IF EXISTS idx_network_members_network_ip").Error; err != nil {
		return fmt.Errorf("drop legacy network member IP index: %w", err)
	}
	if err := handle.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_network_members_network_ip ON network_members (network_id, virtual_ip)").Error; err != nil {
		return fmt.Errorf("create scoped network member IP index: %w", err)
	}
	return nil
}

func parseLogLevel(value string) logger.LogLevel {
	switch value {
	case "debug":
		return logger.Info
	case "warn":
		return logger.Warn
	case "error":
		return logger.Silent
	default:
		return logger.Error
	}
}
