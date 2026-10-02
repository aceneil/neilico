package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"neilico/control-plane/internal/models"
)

const DefaultNodeMetricsWindowHours = 24

type TrafficTotal struct {
	InBytes     int64 `json:"in_bytes"`
	OutBytes    int64 `json:"out_bytes"`
	WindowHours int   `json:"window_hours"`
}

type TrafficPoint struct {
	TS  time.Time `json:"ts"`
	In  int64     `json:"in"`
	Out int64     `json:"out"`
}

type NodeMetrics struct {
	NodeID                     uuid.UUID      `json:"node_id"`
	Status                     string         `json:"status"`
	LastSeen                   *time.Time     `json:"last_seen"`
	HeartbeatIntervalSeconds   int            `json:"heartbeat_interval_seconds"`
	UptimeSecondsSinceRegister float64        `json:"uptime_seconds_since_register"`
	Traffic                    TrafficTotal   `json:"traffic"`
	RecentTraffic              []TrafficPoint `json:"recent_traffic"`
}

type NetworkTunnelSummary struct {
	Total int64  `json:"total"`
	Up    int64  `json:"up"`
	Down  int64  `json:"down"`
	Basis string `json:"basis"`
}

type NetworkStatus struct {
	NetworkID         uuid.UUID            `json:"network_id"`
	MemberCount       int64                `json:"member_count"`
	OnlineMemberCount int64                `json:"online_member_count"`
	Tunnels           NetworkTunnelSummary `json:"tunnels"`
}

type ObservabilityService struct {
	db *gorm.DB
}

func NewObservabilityService(db *gorm.DB) *ObservabilityService {
	return &ObservabilityService{db: db}
}

func (s *ObservabilityService) NodeMetrics(ctx context.Context, nodeID uuid.UUID, tenantID *uuid.UUID, windowHours int) (NodeMetrics, error) {
	query := s.db.WithContext(ctx).Where("id = ?", nodeID)
	if tenantID != nil {
		query = query.Where("tenant_id = ?", *tenantID)
	}
	var node models.Node
	if err := query.First(&node).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return NodeMetrics{}, ErrNotFound
		}
		return NodeMetrics{}, fmt.Errorf("get node for metrics: %w", err)
	}

	now := time.Now().UTC()
	from := now.Add(-time.Duration(windowHours) * time.Hour)
	var trafficLogs []models.TrafficLog
	err := s.db.WithContext(ctx).
		Where("node_id = ? AND created_at >= ? AND created_at <= ? AND direction IN ?", node.ID, from, now, []string{"in", "out"}).
		Order("created_at ASC, id ASC").
		Find(&trafficLogs).Error
	if err != nil {
		return NodeMetrics{}, fmt.Errorf("load node traffic metrics: %w", err)
	}

	total := TrafficTotal{WindowHours: windowHours}
	type bucketKey struct {
		year  int
		month time.Month
		day   int
		hour  int
	}
	buckets := make(map[bucketKey]*TrafficPoint)
	for _, entry := range trafficLogs {
		timestamp := entry.CreatedAt.UTC()
		if entry.Direction == "in" {
			total.InBytes += entry.Bytes
		} else {
			total.OutBytes += entry.Bytes
		}
		key := bucketKey{timestamp.Year(), timestamp.Month(), timestamp.Day(), timestamp.Hour()}
		point := buckets[key]
		if point == nil {
			start := time.Date(timestamp.Year(), timestamp.Month(), timestamp.Day(), timestamp.Hour(), 0, 0, 0, time.UTC)
			point = &TrafficPoint{TS: start}
			buckets[key] = point
		}
		if entry.Direction == "in" {
			point.In += entry.Bytes
		} else {
			point.Out += entry.Bytes
		}
	}

	recent := make([]TrafficPoint, 0, len(buckets))
	for _, point := range buckets {
		recent = append(recent, *point)
	}
	sort.Slice(recent, func(i, j int) bool {
		return recent[i].TS.Before(recent[j].TS)
	})

	var lastSeen *time.Time
	if node.LastSeen != nil {
		value := node.LastSeen.UTC()
		lastSeen = &value
	}
	return NodeMetrics{
		NodeID:                     node.ID,
		Status:                     node.Status,
		LastSeen:                   lastSeen,
		HeartbeatIntervalSeconds:   30,
		UptimeSecondsSinceRegister: now.Sub(node.CreatedAt.UTC()).Seconds(),
		Traffic:                    total,
		RecentTraffic:              recent,
	}, nil
}

func (s *ObservabilityService) NetworkStatus(ctx context.Context, networkID uuid.UUID, tenantID *uuid.UUID) (NetworkStatus, error) {
	query := s.db.WithContext(ctx).Where("id = ?", networkID)
	if tenantID != nil {
		query = query.Where("tenant_id = ?", *tenantID)
	}
	var network models.VirtualNetwork
	if err := query.First(&network).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return NetworkStatus{}, ErrNotFound
		}
		return NetworkStatus{}, fmt.Errorf("get network status: %w", err)
	}

	var memberCount int64
	if err := s.db.WithContext(ctx).Model(&models.NetworkMember{}).
		Where("network_id = ?", network.ID).Count(&memberCount).Error; err != nil {
		return NetworkStatus{}, fmt.Errorf("count network members: %w", err)
	}
	var onlineCount int64
	if err := s.db.WithContext(ctx).Model(&models.NetworkMember{}).
		Joins("JOIN nodes ON nodes.id = network_members.node_id").
		Where("network_members.network_id = ? AND nodes.status = ?", network.ID, NodeStatusOnline).
		Count(&onlineCount).Error; err != nil {
		return NetworkStatus{}, fmt.Errorf("count online network members: %w", err)
	}
	return NetworkStatus{
		NetworkID:         network.ID,
		MemberCount:       memberCount,
		OnlineMemberCount: onlineCount,
		Tunnels: NetworkTunnelSummary{
			Total: memberCount,
			Up:    onlineCount,
			Down:  memberCount - onlineCount,
			Basis: "member_status",
		},
	}, nil
}
