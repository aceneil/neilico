package alerts

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"neilico/control-plane/internal/models"
)

type MetricsObserver interface {
	SetAlertsFiring(map[string]int64)
}

type Engine struct {
	db       *gorm.DB
	options  Options
	notifier Notifier
	metrics  MetricsObserver
	logger   *slog.Logger
}

func NewEngine(db *gorm.DB, options Options, notifier Notifier, observer MetricsObserver, logger *slog.Logger) *Engine {
	if logger == nil {
		logger = slog.Default()
	}
	if notifier == nil {
		notifier = NewLogNotifier(logger)
	}
	return &Engine{
		db: db, options: options.normalized(), notifier: notifier,
		metrics: observer, logger: logger,
	}
}

func (e *Engine) Options() Options {
	return e.options
}

// Evaluate executes one complete rule pass for a tenant. It returns state
// transitions plus continuously firing observations. Insufficient data is
// explicitly represented and is never persisted or notified as a real alert.
func (e *Engine) Evaluate(ctx context.Context, tenantID uuid.UUID) []Alert {
	if tenantID == uuid.Nil {
		return nil
	}
	now := time.Now().UTC()
	candidates, unavailable := e.evaluateRules(ctx, tenantID.String(), now)
	active := make(map[string]struct{}, len(candidates))
	results := make([]Alert, 0, len(candidates))

	for _, candidate := range candidates {
		if candidate.DataStatus == DataStatusInsufficientData {
			candidate.EvaluatedAt = &now
			results = append(results, candidate)
			continue
		}
		key := candidate.Rule + "|" + candidate.TargetType + "|" + candidate.TargetID
		active[key] = struct{}{}
		result, transition, err := e.upsert(ctx, tenantID, candidate, now)
		if err != nil {
			e.logger.Error("persist alert evaluation", "rule", candidate.Rule, "target_id", candidate.TargetID, "error", err)
			continue
		}
		result.EvaluatedAt = &now
		results = append(results, result)
		if transition {
			e.notify(ctx, "alert.firing", result)
		}
	}

	resolved, err := e.resolveMissing(ctx, tenantID, active, unavailable, now)
	if err != nil {
		e.logger.Error("resolve inactive alerts", "tenant_id", tenantID, "error", err)
	} else {
		results = append(results, resolved...)
	}
	e.refreshMetrics(ctx)
	return results
}

func (e *Engine) EvaluateAll(ctx context.Context) {
	var tenants []models.Tenant
	if err := e.db.WithContext(ctx).Order("created_at ASC, id ASC").Find(&tenants).Error; err != nil {
		e.logger.Error("load tenants for alert evaluation", "error", err)
		return
	}
	for _, tenant := range tenants {
		if ctx.Err() != nil {
			return
		}
		e.Evaluate(ctx, tenant.ID)
	}
	e.cleanupResolved(ctx)
}

func (e *Engine) Run(ctx context.Context) {
	e.EvaluateAll(ctx)
	ticker := time.NewTicker(e.options.EvaluationInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			e.logger.Info("alert evaluator stopped", "reason", ctx.Err())
			return
		case <-ticker.C:
			e.EvaluateAll(ctx)
		}
	}
}

func (e *Engine) upsert(ctx context.Context, tenantID uuid.UUID, candidate Alert, now time.Time) (Alert, bool, error) {
	targetID, err := uuid.Parse(candidate.TargetID)
	if err != nil {
		return Alert{}, false, fmt.Errorf("parse target ID: %w", err)
	}
	var transition bool
	var stored models.Alert
	err = e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND rule = ? AND target_type = ? AND target_id = ?", tenantID, candidate.Rule, candidate.TargetType, targetID)
		result := query.First(&stored)
		if result.Error != nil && !isNotFound(result.Error) {
			return result.Error
		}
		if isNotFound(result.Error) {
			stored = models.Alert{
				ID: uuid.New(), TenantID: tenantID, Rule: candidate.Rule,
				Severity: candidate.Severity, TargetType: candidate.TargetType,
				TargetID: targetID, Title: candidate.Title, Detail: candidate.Detail,
				Value: candidate.Value, Threshold: candidate.Threshold,
				Since: now, StartedAt: now, State: StateFiring,
				EvaluationCount: 1, LastEvaluatedAt: now, CreatedAt: now, UpdatedAt: now,
			}
			if err := tx.Create(&stored).Error; err != nil {
				return err
			}
			transition = true
			return e.createEvent(tx, &stored, now)
		}
		if stored.State != StateFiring {
			transition = true
			stored.StartedAt = now
			stored.ResolvedAt = nil
		}
		stored.Severity = candidate.Severity
		stored.Title = candidate.Title
		stored.Detail = candidate.Detail
		stored.Value = candidate.Value
		stored.Threshold = candidate.Threshold
		stored.Since = now
		stored.State = StateFiring
		stored.EvaluationCount++
		stored.LastEvaluatedAt = now
		stored.UpdatedAt = now
		if err := tx.Save(&stored).Error; err != nil {
			return err
		}
		if transition {
			return e.createEvent(tx, &stored, now)
		}
		return nil
	})
	if err != nil {
		return Alert{}, false, err
	}
	return alertFromModel(stored), transition, nil
}

func (e *Engine) resolveMissing(ctx context.Context, tenantID uuid.UUID, active map[string]struct{}, unavailable map[string]struct{}, now time.Time) ([]Alert, error) {
	var firing []models.Alert
	if err := e.db.WithContext(ctx).
		Where("tenant_id = ? AND state = ?", tenantID, StateFiring).
		Find(&firing).Error; err != nil {
		return nil, err
	}
	results := make([]Alert, 0)
	for _, item := range firing {
		if _, ok := unavailable[item.Rule]; ok {
			continue
		}
		key := item.Rule + "|" + item.TargetType + "|" + item.TargetID.String()
		if _, ok := active[key]; ok {
			continue
		}
		stored := item
		err := e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&stored, "id = ?", item.ID).Error; err != nil {
				return err
			}
			if stored.State != StateFiring {
				return nil
			}
			stored.State = StateResolved
			stored.ResolvedAt = &now
			stored.Since = now
			stored.LastEvaluatedAt = now
			stored.UpdatedAt = now
			if err := tx.Save(&stored).Error; err != nil {
				return err
			}
			return e.createEvent(tx, &stored, now)
		})
		if err != nil {
			e.logger.Error("resolve alert", "alert_id", item.ID, "error", err)
			continue
		}
		result := alertFromModel(stored)
		result.EvaluatedAt = &now
		results = append(results, result)
		e.notify(ctx, "alert.resolved", result)
	}
	return results, nil
}

func (e *Engine) createEvent(tx *gorm.DB, alert *models.Alert, now time.Time) error {
	return tx.Create(&models.AlertEvent{
		ID: uuid.New(), AlertID: alert.ID, TenantID: alert.TenantID,
		Rule: alert.Rule, TargetType: alert.TargetType, TargetID: alert.TargetID,
		State: alert.State, Severity: alert.Severity, Value: alert.Value,
		Threshold: alert.Threshold, Detail: alert.Detail, CreatedAt: now,
	}).Error
}

func (e *Engine) notify(ctx context.Context, event string, alert Alert) {
	notification := Notification{Event: event, Schema: "neilico.alert.v1", Alert: alert, Timestamp: time.Now().UTC()}
	if err := e.notifier.Notify(ctx, notification); err != nil {
		// Notification failures must never fail evaluation or the API request.
		e.logger.Error("alert notification failed", "event", event, "alert_id", alert.ID, "rule", alert.Rule, "error", err)
	}
}

func (e *Engine) List(ctx context.Context, filter ListFilter) (List, error) {
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 || filter.PageSize > 100 {
		filter.PageSize = 20
	}
	query := e.db.WithContext(ctx).Model(&models.Alert{})
	if filter.TenantID != nil {
		query = query.Where("tenant_id = ?", *filter.TenantID)
	}
	if filter.State != "" {
		query = query.Where("state = ?", filter.State)
	}
	if filter.Severity != "" {
		query = query.Where("severity = ?", filter.Severity)
	}
	if filter.Rule != "" {
		query = query.Where("rule = ?", filter.Rule)
	}
	if filter.TargetType != "" {
		query = query.Where("target_type = ?", filter.TargetType)
	}
	if filter.TargetID != nil {
		query = query.Where("target_id = ?", *filter.TargetID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return List{}, fmt.Errorf("count alerts: %w", err)
	}
	rows := make([]models.Alert, 0)
	err := query.Order("CASE WHEN state = 'firing' THEN 0 ELSE 1 END, since DESC, id ASC").
		Offset((filter.Page - 1) * filter.PageSize).Limit(filter.PageSize).Find(&rows).Error
	if err != nil {
		return List{}, fmt.Errorf("list alerts: %w", err)
	}
	items := make([]Alert, 0, len(rows))
	for _, row := range rows {
		items = append(items, alertFromModel(row))
	}
	return List{Items: items, Total: total, Page: filter.Page, PageSize: filter.PageSize}, nil
}

func (e *Engine) Get(ctx context.Context, id uuid.UUID, tenantID *uuid.UUID) (Alert, []AlertEvent, error) {
	query := e.db.WithContext(ctx).Where("id = ?", id)
	if tenantID != nil {
		query = query.Where("tenant_id = ?", *tenantID)
	}
	var stored models.Alert
	if err := query.First(&stored).Error; err != nil {
		if isNotFound(err) {
			return Alert{}, nil, gorm.ErrRecordNotFound
		}
		return Alert{}, nil, err
	}
	var rows []models.AlertEvent
	if err := e.db.WithContext(ctx).Where("alert_id = ?", id).
		Order("created_at ASC, id ASC").Find(&rows).Error; err != nil {
		return Alert{}, nil, err
	}
	events := make([]AlertEvent, 0, len(rows))
	for _, row := range rows {
		events = append(events, eventFromModel(row))
	}
	return alertFromModel(stored), events, nil
}

func (e *Engine) Summary(ctx context.Context, tenantID *uuid.UUID) (Summary, error) {
	query := e.db.WithContext(ctx).Model(&models.Alert{})
	if tenantID != nil {
		query = query.Where("tenant_id = ?", *tenantID)
	}
	summary := Summary{ByRule: map[string]int64{}}
	rows := make([]models.Alert, 0)
	if err := query.Find(&rows).Error; err != nil {
		return Summary{}, err
	}
	recentCutoff := time.Now().UTC().Add(-24 * time.Hour)
	for _, row := range rows {
		if row.State == StateFiring {
			switch row.Severity {
			case SeverityCritical:
				summary.Firing.Critical++
			case SeverityWarning:
				summary.Firing.Warning++
			default:
				summary.Firing.Info++
			}
			summary.ByRule[row.Rule]++
		} else if row.ResolvedAt != nil && row.ResolvedAt.After(recentCutoff) {
			summary.ResolvedRecent++
		}
	}
	return summary, nil
}

func (e *Engine) cleanupResolved(ctx context.Context) {
	cutoff := time.Now().UTC().Add(-e.options.ResolvedRetention)
	if err := e.db.WithContext(ctx).
		Where("state = ? AND resolved_at IS NOT NULL AND resolved_at < ?", StateResolved, cutoff).
		Delete(&models.Alert{}).Error; err != nil {
		e.logger.Error("cleanup resolved alerts", "error", err)
	}
}

func (e *Engine) refreshMetrics(ctx context.Context) {
	if e.metrics == nil {
		return
	}
	type countRow struct {
		Rule     string
		Severity string
		Count    int64
	}
	rows := make([]countRow, 0)
	if err := e.db.WithContext(ctx).Model(&models.Alert{}).
		Select("rule, severity, COUNT(*) AS count").
		Where("state = ?", StateFiring).
		Group("rule, severity").Scan(&rows).Error; err != nil {
		e.logger.Error("refresh alert metrics", "error", err)
		return
	}
	counts := make(map[string]int64, len(rows))
	for _, row := range rows {
		counts[row.Rule+"|"+row.Severity] = row.Count
	}
	e.metrics.SetAlertsFiring(counts)
}

func alertFromModel(row models.Alert) Alert {
	started := row.StartedAt.UTC()
	result := Alert{
		ID: row.ID.String(), Rule: row.Rule, Severity: row.Severity,
		TargetType: row.TargetType, TargetID: row.TargetID.String(),
		Title: row.Title, Detail: row.Detail, Value: row.Value,
		Threshold: row.Threshold, Since: row.Since.UTC(), State: row.State,
		StartedAt: &started, TenantID: row.TenantID.String(), DataStatus: DataStatusAvailable,
	}
	if row.ResolvedAt != nil {
		value := row.ResolvedAt.UTC()
		result.ResolvedAt = &value
	}
	return result
}

func eventFromModel(row models.AlertEvent) AlertEvent {
	return AlertEvent{
		ID: row.ID.String(), AlertID: row.AlertID.String(), Rule: row.Rule,
		TargetType: row.TargetType, TargetID: row.TargetID.String(), State: row.State,
		Severity: row.Severity, Value: row.Value, Threshold: row.Threshold,
		Detail: row.Detail, CreatedAt: row.CreatedAt.UTC(),
	}
}

func isNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), gorm.ErrRecordNotFound.Error())
}
