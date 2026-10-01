package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"umpp/control-plane/internal/models"
)

const RelayOnlineWindow = 60 * time.Second

type RelayServerInput struct {
	Name     string     `json:"name"`
	Endpoint string     `json:"endpoint"`
	Region   string     `json:"region"`
	Status   *string    `json:"status,omitempty"`
	LastSeen *time.Time `json:"last_seen,omitempty"`
}

type RelayServerView struct {
	ID       uuid.UUID  `json:"id"`
	Name     string     `json:"name"`
	Endpoint string     `json:"endpoint"`
	Region   string     `json:"region"`
	Status   string     `json:"status"`
	LastSeen *time.Time `json:"last_seen"`
}

type RelayServerList struct {
	Items []RelayServerView `json:"items"`
	Total int64             `json:"total"`
}

type RelayServerService struct {
	db *gorm.DB
}

func NewRelayServerService(db *gorm.DB) *RelayServerService {
	return &RelayServerService{db: db}
}

func validateRelayEndpoint(endpoint string) error {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return errors.New("endpoint is required")
	}
	if strings.Contains(endpoint, "://") || strings.ContainsAny(endpoint, "/\\@ \t\r\n") {
		return errors.New("endpoint must use host:port format")
	}
	host, rawPort, err := net.SplitHostPort(endpoint)
	if err != nil || strings.TrimSpace(host) == "" {
		return errors.New("endpoint must use host:port format")
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("endpoint port must be between 1 and 65535")
	}
	if strings.ContainsAny(host, " \t\r\n") {
		return errors.New("endpoint host must not contain whitespace")
	}
	return nil
}

func relayView(item models.RelayServer, now time.Time) RelayServerView {
	status := "offline"
	if item.LastSeen != nil && !item.LastSeen.IsZero() {
		age := now.Sub(item.LastSeen.UTC())
		if age >= 0 && age <= RelayOnlineWindow {
			status = "online"
		}
	}
	var lastSeen *time.Time
	if item.LastSeen != nil {
		value := item.LastSeen.UTC()
		lastSeen = &value
	}
	return RelayServerView{
		ID:       item.ID,
		Name:     item.Name,
		Endpoint: item.Endpoint,
		Region:   item.Region,
		Status:   status,
		LastSeen: lastSeen,
	}
}

func normalizeRelayInput(input RelayServerInput) (RelayServerInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Endpoint = strings.TrimSpace(input.Endpoint)
	input.Region = strings.TrimSpace(input.Region)
	if input.Name == "" {
		return RelayServerInput{}, fmt.Errorf("%w: name is required", ErrInvalidInput)
	}
	if len(input.Name) > 255 {
		return RelayServerInput{}, fmt.Errorf("%w: name must not exceed 255 characters", ErrInvalidInput)
	}
	if err := validateRelayEndpoint(input.Endpoint); err != nil {
		return RelayServerInput{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if input.Region == "" {
		return RelayServerInput{}, fmt.Errorf("%w: region is required", ErrInvalidInput)
	}
	if len(input.Region) > 64 {
		return RelayServerInput{}, fmt.Errorf("%w: region must not exceed 64 characters", ErrInvalidInput)
	}
	return input, nil
}

func (s *RelayServerService) List(ctx context.Context) (RelayServerList, error) {
	var items []models.RelayServer
	if err := s.db.WithContext(ctx).Order("name ASC, id ASC").Find(&items).Error; err != nil {
		return RelayServerList{}, fmt.Errorf("list relay servers: %w", err)
	}
	now := time.Now().UTC()
	views := make([]RelayServerView, 0, len(items))
	for _, item := range items {
		views = append(views, relayView(item, now))
	}
	return RelayServerList{Items: views, Total: int64(len(views))}, nil
}

func (s *RelayServerService) Create(ctx context.Context, input RelayServerInput) (RelayServerView, error) {
	normalized, err := normalizeRelayInput(input)
	if err != nil {
		return RelayServerView{}, err
	}
	item := models.RelayServer{
		ID:       uuid.New(),
		Name:     normalized.Name,
		Endpoint: normalized.Endpoint,
		Region:   normalized.Region,
		Status:   "offline",
		LastSeen: normalized.LastSeen,
	}
	if err := s.db.WithContext(ctx).Create(&item).Error; err != nil {
		if isUniqueViolation(err) {
			return RelayServerView{}, ErrConflict
		}
		return RelayServerView{}, fmt.Errorf("create relay server: %w", err)
	}
	return relayView(item, time.Now().UTC()), nil
}

func (s *RelayServerService) Update(ctx context.Context, id uuid.UUID, input RelayServerInput) (RelayServerView, error) {
	normalized, err := normalizeRelayInput(input)
	if err != nil {
		return RelayServerView{}, err
	}
	var item models.RelayServer
	if err := s.db.WithContext(ctx).First(&item, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return RelayServerView{}, ErrNotFound
		}
		return RelayServerView{}, fmt.Errorf("get relay server: %w", err)
	}
	var conflictCount int64
	if err := s.db.WithContext(ctx).Model(&models.RelayServer{}).
		Where("name = ? AND id <> ?", normalized.Name, item.ID).
		Count(&conflictCount).Error; err != nil {
		return RelayServerView{}, fmt.Errorf("check relay server name: %w", err)
	}
	if conflictCount > 0 {
		return RelayServerView{}, ErrConflict
	}
	item.Name = normalized.Name
	item.Endpoint = normalized.Endpoint
	item.Region = normalized.Region
	item.LastSeen = normalized.LastSeen
	if err := s.db.WithContext(ctx).Save(&item).Error; err != nil {
		if isUniqueViolation(err) {
			return RelayServerView{}, ErrConflict
		}
		return RelayServerView{}, fmt.Errorf("update relay server: %w", err)
	}
	return relayView(item, time.Now().UTC()), nil
}

func (s *RelayServerService) Delete(ctx context.Context, id uuid.UUID) error {
	result := s.db.WithContext(ctx).Delete(&models.RelayServer{}, "id = ?", id)
	if result.Error != nil {
		return fmt.Errorf("delete relay server: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
