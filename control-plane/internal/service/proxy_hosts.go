package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"neilico/control-plane/internal/models"
)

// ProxyHostService 提供 Nginx Proxy Manager 风格的「代理主机」复合操作：
// 一行 = 一个域名 + 它的默认代理规则。底层仍然是 Domain / ProxyRule 两张表，
// 这里只做「一次提交同时建域名与默认规则」的事务封装，不新增表、不改旧模型。
//
// 复用而非重写：内部通过把同一个事务句柄（tx）注入 DomainService / ProxyRuleService，
// 让它们各自既有的校验与持久化逻辑在事务内运行；任一步失败则整体回滚，不留半成品。
type ProxyHostService struct {
	db *gorm.DB
}

func NewProxyHostService(db *gorm.DB) *ProxyHostService { return &ProxyHostService{db: db} }

// ProxyHostInput 是一次提交即可完成「域名 → 地址:端口」绑定的复合入参。
// TargetType/Target 直接沿用既有 validation.Target 语义：
//   - internal_ip：<内网物理IP>:<端口>
//   - virtual_ip ：<虚拟IP>:<端口>（走 Mesh 隧道）
//   - node       ：<节点UUID>:<端口>（控制面解析为节点当前虚拟 IP）
type ProxyHostInput struct {
	Domain                     string               `json:"domain"`
	CertID                     *uuid.UUID           `json:"cert_id,omitempty"`
	Status                     string               `json:"status,omitempty"`
	Path                       string               `json:"path,omitempty"`
	TargetType                 string               `json:"target_type"`
	Target                     string               `json:"target"`
	UpstreamScheme             string               `json:"upstream_scheme,omitempty"`
	UpstreamInsecureSkipVerify bool                 `json:"upstream_insecure_skip_verify,omitempty"`
	UpstreamCAFile             string               `json:"upstream_ca_file,omitempty"`
	AccessControl              models.AccessControl `json:"access_control"`
	Enabled                    *bool                `json:"enabled,omitempty"`
}

// ProxyHost 是合并后的「一行一个主机」视图：域名身份来自 Domain，转发目标来自其默认规则。
// RuleID 为空表示该域名尚未绑定任何代理规则（如通过旧的「域名」表单独创建）。
type ProxyHost struct {
	ID                         uuid.UUID            `json:"id"` // = domain_id（主机身份）
	DomainID                   uuid.UUID            `json:"domain_id"`
	TenantID                   uuid.UUID            `json:"tenant_id"`
	Domain                     string               `json:"domain"`
	CertID                     *uuid.UUID           `json:"cert_id"`
	Status                     string               `json:"status"`
	RuleID                     *uuid.UUID           `json:"rule_id"`
	Path                       string               `json:"path"`
	TargetType                 string               `json:"target_type"`
	Target                     string               `json:"target"`
	UpstreamScheme             string               `json:"upstream_scheme"`
	UpstreamInsecureSkipVerify bool                 `json:"upstream_insecure_skip_verify"`
	UpstreamCAFile             string               `json:"upstream_ca_file,omitempty"`
	AccessControl              models.AccessControl `json:"access_control"`
	Enabled                    bool                 `json:"enabled"`
	RuleCount                  int                  `json:"rule_count"`
	CreatedAt                  time.Time            `json:"created_at"`
}

type ProxyHostList struct {
	Items    []ProxyHost `json:"items"`
	Total    int64       `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
}

const defaultProxyHostPath = "/"

func (s *ProxyHostService) List(ctx context.Context, tenantID *uuid.UUID, page, pageSize int) (ProxyHostList, error) {
	query := s.db.WithContext(ctx).Model(&models.Domain{})
	if tenantID != nil {
		query = query.Where("tenant_id = ?", *tenantID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return ProxyHostList{}, fmt.Errorf("count proxy hosts: %w", err)
	}
	var domains []models.Domain
	if err := query.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&domains).Error; err != nil {
		return ProxyHostList{}, fmt.Errorf("list proxy hosts: %w", err)
	}
	rulesByDomain, err := s.rulesByDomain(ctx, domains)
	if err != nil {
		return ProxyHostList{}, err
	}
	items := make([]ProxyHost, 0, len(domains))
	for _, domain := range domains {
		items = append(items, buildProxyHost(domain, rulesByDomain[domain.ID]))
	}
	return ProxyHostList{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

func (s *ProxyHostService) Get(ctx context.Context, id uuid.UUID, tenantID *uuid.UUID) (ProxyHost, error) {
	domain, err := loadProxyHostDomain(ctx, s.db, id, tenantID)
	if err != nil {
		return ProxyHost{}, err
	}
	rules, err := loadProxyHostRules(ctx, s.db, domain.ID)
	if err != nil {
		return ProxyHost{}, err
	}
	return buildProxyHost(domain, rules), nil
}

// Create 在一个事务里同时落盘 Domain 与其默认 ProxyRule；任一步失败即回滚，保证不产生半成品。
func (s *ProxyHostService) Create(ctx context.Context, tenantID uuid.UUID, input ProxyHostInput) (ProxyHost, error) {
	var host ProxyHost
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		domainSvc := NewDomainService(tx)
		ruleSvc := NewProxyRuleService(tx)
		status := strings.TrimSpace(input.Status)
		if status == "" {
			status = DomainStatusActive
		}
		domain, err := domainSvc.Create(ctx, tenantID, DomainInput{
			Domain: input.Domain,
			CertID: input.CertID,
			Status: status,
		})
		if err != nil {
			return err
		}
		rule, err := ruleSvc.Create(ctx, tenantID, proxyHostRuleInput(domain.ID, input))
		if err != nil {
			return err
		}
		host = buildProxyHost(domain, []models.ProxyRule{rule})
		return nil
	})
	if err != nil {
		return ProxyHost{}, err
	}
	return host, nil
}

// Update 在同一个事务里同时更新域名与它的默认代理规则（能一次改域名与转发目标）。
// 若该域名此前没有规则（例如由旧「域名」表单独创建），则就地补建默认规则。
func (s *ProxyHostService) Update(ctx context.Context, id uuid.UUID, tenantID *uuid.UUID, input ProxyHostInput) (ProxyHost, error) {
	var host ProxyHost
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		domainSvc := NewDomainService(tx)
		ruleSvc := NewProxyRuleService(tx)
		current, err := loadProxyHostDomain(ctx, tx, id, tenantID)
		if err != nil {
			return err
		}
		// 域名表单不管理证书：未显式传入时保留原绑定，避免编辑转发目标意外清空 TLS 绑定。
		certID := input.CertID
		if certID == nil {
			certID = current.CertID
		}
		status := strings.TrimSpace(input.Status)
		if status == "" {
			status = current.Status
		}
		if status == "" {
			status = DomainStatusActive
		}
		domain, err := domainSvc.Update(ctx, id, tenantID, DomainInput{
			Domain: input.Domain,
			CertID: certID,
			Status: status,
		})
		if err != nil {
			return err
		}
		existing, err := loadProxyHostRules(ctx, tx, domain.ID)
		if err != nil {
			return err
		}
		ruleInput := proxyHostRuleInput(domain.ID, input)
		var rule models.ProxyRule
		if representative := representativeRule(existing); representative != nil {
			rule, err = ruleSvc.Update(ctx, representative.ID, tenantID, ruleInput)
		} else {
			rule, err = ruleSvc.Create(ctx, domain.TenantID, ruleInput)
		}
		if err != nil {
			return err
		}
		host = buildProxyHost(domain, replaceRule(existing, rule))
		return nil
	})
	if err != nil {
		return ProxyHost{}, err
	}
	return host, nil
}

// Delete 级联清理：先删该域名下的全部代理规则，再删域名（顺应 ProxyRule→Domain 的 RESTRICT 外键）。
// 整个过程在一个事务里，要么全删要么全不动，不会因残留引用兜成 500。
func (s *ProxyHostService) Delete(ctx context.Context, id uuid.UUID, tenantID *uuid.UUID) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := loadProxyHostDomain(ctx, tx, id, tenantID); err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Where("domain_id = ?", id).Delete(&models.ProxyRule{}).Error; err != nil {
			return fmt.Errorf("delete proxy host rules: %w", err)
		}
		if err := tx.WithContext(ctx).Delete(&models.Domain{}, "id = ?", id).Error; err != nil {
			return fmt.Errorf("delete proxy host: %w", err)
		}
		return nil
	})
}

// RulesForHost 返回该主机的租户与其全部代理规则 ID。
// 删除前先用它落盘配置版本（BumpForProxy 需要规则仍在库中）。
func (s *ProxyHostService) RulesForHost(ctx context.Context, id uuid.UUID, tenantID *uuid.UUID) (uuid.UUID, []uuid.UUID, error) {
	domain, err := loadProxyHostDomain(ctx, s.db, id, tenantID)
	if err != nil {
		return uuid.Nil, nil, err
	}
	rules, err := loadProxyHostRules(ctx, s.db, domain.ID)
	if err != nil {
		return uuid.Nil, nil, err
	}
	ids := make([]uuid.UUID, 0, len(rules))
	for _, rule := range rules {
		ids = append(ids, rule.ID)
	}
	return domain.TenantID, ids, nil
}

// loadProxyHostDomain 以给定句柄（根连接或事务）读取域名，并做租户范围校验。
// 事务内必须传入 tx，否则会另开连接与写事务互锁。
func loadProxyHostDomain(ctx context.Context, db *gorm.DB, id uuid.UUID, tenantID *uuid.UUID) (models.Domain, error) {
	query := db.WithContext(ctx).Where("id = ?", id)
	if tenantID != nil {
		query = query.Where("tenant_id = ?", *tenantID)
	}
	var domain models.Domain
	err := query.First(&domain).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.Domain{}, ErrNotFound
	}
	if err != nil {
		return models.Domain{}, fmt.Errorf("get proxy host: %w", err)
	}
	return domain, nil
}

func loadProxyHostRules(ctx context.Context, db *gorm.DB, domainID uuid.UUID) ([]models.ProxyRule, error) {
	var rules []models.ProxyRule
	if err := db.WithContext(ctx).Where("domain_id = ?", domainID).Order("created_at ASC").Find(&rules).Error; err != nil {
		return nil, fmt.Errorf("list proxy host rules: %w", err)
	}
	return rules, nil
}

func (s *ProxyHostService) rulesByDomain(ctx context.Context, domains []models.Domain) (map[uuid.UUID][]models.ProxyRule, error) {
	result := make(map[uuid.UUID][]models.ProxyRule, len(domains))
	if len(domains) == 0 {
		return result, nil
	}
	ids := make([]uuid.UUID, 0, len(domains))
	for _, domain := range domains {
		ids = append(ids, domain.ID)
	}
	var rules []models.ProxyRule
	if err := s.db.WithContext(ctx).Where("domain_id IN ?", ids).Order("created_at ASC").Find(&rules).Error; err != nil {
		return nil, fmt.Errorf("list proxy host rules: %w", err)
	}
	for _, rule := range rules {
		result[rule.DomainID] = append(result[rule.DomainID], rule)
	}
	return result, nil
}

// proxyHostRuleInput 把复合入参映射为底层代理规则入参；path 缺省为「/」。
func proxyHostRuleInput(domainID uuid.UUID, input ProxyHostInput) ProxyRuleInput {
	path := strings.TrimSpace(input.Path)
	if path == "" {
		path = defaultProxyHostPath
	}
	return ProxyRuleInput{
		DomainID:                   domainID,
		Path:                       path,
		TargetType:                 input.TargetType,
		Target:                     input.Target,
		AccessControl:              input.AccessControl,
		UpstreamScheme:             input.UpstreamScheme,
		UpstreamInsecureSkipVerify: input.UpstreamInsecureSkipVerify,
		UpstreamCAFile:             input.UpstreamCAFile,
		Enabled:                    input.Enabled,
	}
}

// representativeRule 选出代表该主机的「默认规则」：优先 path=「/」，否则取最早创建的一条。
func representativeRule(rules []models.ProxyRule) *models.ProxyRule {
	if len(rules) == 0 {
		return nil
	}
	for index := range rules {
		if rules[index].Path == defaultProxyHostPath {
			return &rules[index]
		}
	}
	return &rules[0]
}

// replaceRule 用新规则替换同 ID 的旧记录（不存在则追加），用于合并后的主机视图。
func replaceRule(rules []models.ProxyRule, updated models.ProxyRule) []models.ProxyRule {
	for index := range rules {
		if rules[index].ID == updated.ID {
			rules[index] = updated
			return rules
		}
	}
	return append(rules, updated)
}

func buildProxyHost(domain models.Domain, rules []models.ProxyRule) ProxyHost {
	host := ProxyHost{
		ID:             domain.ID,
		DomainID:       domain.ID,
		TenantID:       domain.TenantID,
		Domain:         domain.Domain,
		CertID:         domain.CertID,
		Status:         domain.Status,
		Path:           defaultProxyHostPath,
		UpstreamScheme: "http",
		Enabled:        true,
		RuleCount:      len(rules),
		AccessControl:  models.AccessControl{IPWhitelist: []string{}},
		CreatedAt:      domain.CreatedAt,
	}
	if rule := representativeRule(rules); rule != nil {
		ruleID := rule.ID
		host.RuleID = &ruleID
		host.Path = rule.Path
		host.TargetType = rule.TargetType
		host.Target = rule.Target
		host.UpstreamScheme = rule.UpstreamScheme
		host.UpstreamInsecureSkipVerify = rule.UpstreamInsecureSkipVerify
		host.UpstreamCAFile = rule.UpstreamCAFile
		host.AccessControl = rule.AccessControl
		host.Enabled = rule.Enabled
	}
	return host
}
