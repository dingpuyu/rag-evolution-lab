package datasetaccess

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/dingpuyu/rag-evolution-lab/internal/auth"
)

var (
	ErrDatasetNotFound = errors.New("dataset not found")
	ErrDatasetDenied   = errors.New("dataset access denied")
)

type Dataset struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Product      string   `json:"-"`
	Visibility   string   `json:"visibility"`
	OwnerTenant  string   `json:"owner_tenant,omitempty"`
	AllowedRoles []string `json:"allowed_roles,omitempty"`
	Status       string   `json:"status"`
	CreatedBy    string   `json:"created_by,omitempty"`
}

type CreateDataset struct {
	Name         string   `json:"name"`
	Slug         string   `json:"slug"`
	Description  string   `json:"description"`
	Visibility   string   `json:"visibility"`
	AllowedRoles []string `json:"allowed_roles,omitempty"`
}

type Membership struct {
	TenantID string `json:"tenant_id"`
	Subject  string `json:"subject"`
	Role     string `json:"role"`
	Status   string `json:"status"`
}

type Status struct {
	Backend     string `json:"backend"`
	Connected   bool   `json:"connected"`
	Tenants     int64  `json:"tenants"`
	Users       int64  `json:"users"`
	Memberships int64  `json:"memberships"`
	Datasets    int64  `json:"datasets"`
}

type Store interface {
	EnsureIdentity(context.Context, auth.Identity) error
	Visible(context.Context, auth.Identity) ([]Dataset, error)
	Authorize(context.Context, string, auth.Identity) (Dataset, error)
	Create(context.Context, auth.Identity, CreateDataset) (Dataset, error)
	Members(context.Context, auth.Identity) ([]Membership, error)
	Status(context.Context, auth.Identity) (Status, error)
}

type Catalog struct {
	byID map[string]Dataset
}

func New(datasets []Dataset) *Catalog {
	catalog := &Catalog{byID: make(map[string]Dataset, len(datasets))}
	for _, dataset := range datasets {
		catalog.byID[dataset.ID] = dataset
	}
	return catalog
}

func Defaults() *Catalog {
	return New([]Dataset{
		{ID: "public-identity", Name: "身份与单点登录", Description: "公开的 SSO、SAML 与 OIDC 产品文档", Product: "identity", Visibility: "public", Status: "active"},
		{ID: "public-reports", Name: "报表中心", Description: "公开的报表导出与权限说明", Product: "reports", Visibility: "public", Status: "active"},
		{ID: "public-api-platform", Name: "API 与开发者平台", Description: "API 限流、错误码与重试规范", Product: "api-gateway", Visibility: "public", Status: "active"},
		{ID: "public-storage", Name: "文件与存储", Description: "文件上传、备份与跨区域复制说明", Product: "storage", Visibility: "public", Status: "active"},
		{ID: "public-billing", Name: "套餐与计费", Description: "套餐能力、额度和计费规则", Product: "billing", Visibility: "public", Status: "active"},
		{ID: "public-operations", Name: "服务运行与故障", Description: "服务状态、事件编号与故障判断指南", Product: "operations", Visibility: "public", Status: "active"},
		{ID: "public-security", Name: "安全与审计", Description: "MFA、最小权限和审计留痕规范", Product: "security", Visibility: "public", Status: "active"},
		{ID: "public-integrations", Name: "集成与 Webhook", Description: "Webhook 签名、幂等和重试约定", Product: "integrations", Visibility: "public", Status: "active"},
		{ID: "public-medical-device", Name: "PulseCare 公共设备资料", Description: "虚构医疗设备的手册、错误码、版本说明和现场更正通知", Product: "medical-device-public", Visibility: "public", Status: "active"},
		{ID: "tenant-a-operations", Name: "Tenant A 运维知识库", Description: "Tenant A 管理员专属运行手册", Product: "tenant-operations", Visibility: "tenant", OwnerTenant: "tenant_a", AllowedRoles: []string{"admin"}, Status: "active"},
		{ID: "tenant-b-operations", Name: "Tenant B 运维知识库", Description: "Tenant B 管理员专属运行手册", Product: "tenant-operations", Visibility: "tenant", OwnerTenant: "tenant_b", AllowedRoles: []string{"admin"}, Status: "active"},
		{ID: "tenant-a-medical-runbook", Name: "Tenant A 医疗设备运维 Runbook", Description: "Tenant A 医学工程团队专属的虚构运维资料", Product: "medical-device-private", Visibility: "tenant", OwnerTenant: "tenant_a", AllowedRoles: []string{"admin"}, Status: "active"},
		{ID: "tenant-b-medical-runbook", Name: "Tenant B 医疗设备运维 Runbook", Description: "Tenant B 医学工程团队专属的虚构运维资料", Product: "medical-device-private", Visibility: "tenant", OwnerTenant: "tenant_b", AllowedRoles: []string{"admin"}, Status: "active"},
	})
}

func (catalog *Catalog) EnsureIdentity(context.Context, auth.Identity) error { return nil }

func (catalog *Catalog) Visible(_ context.Context, identity auth.Identity) ([]Dataset, error) {
	result := make([]Dataset, 0, len(catalog.byID))
	for _, dataset := range catalog.byID {
		if catalog.allowed(dataset, identity) {
			result = append(result, dataset)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func (catalog *Catalog) Authorize(_ context.Context, id string, identity auth.Identity) (Dataset, error) {
	dataset, ok := catalog.byID[strings.TrimSpace(id)]
	if !ok {
		return Dataset{}, ErrDatasetNotFound
	}
	if !catalog.allowed(dataset, identity) {
		return Dataset{}, ErrDatasetDenied
	}
	return dataset, nil
}

func (catalog *Catalog) Create(_ context.Context, _ auth.Identity, _ CreateDataset) (Dataset, error) {
	return Dataset{}, errors.New("in-memory catalog is read-only")
}

func (catalog *Catalog) Members(_ context.Context, identity auth.Identity) ([]Membership, error) {
	return []Membership{{
		TenantID: identity.TenantID, Subject: identity.Subject,
		Role: identity.PrimaryRole(), Status: "active",
	}}, nil
}

func (catalog *Catalog) Status(ctx context.Context, identity auth.Identity) (Status, error) {
	visible, _ := catalog.Visible(ctx, identity)
	return Status{
		Backend: "memory", Connected: true, Tenants: 1, Users: 1,
		Memberships: 1, Datasets: int64(len(visible)),
	}, nil
}

func (catalog *Catalog) allowed(dataset Dataset, identity auth.Identity) bool {
	if identity.HasRole("platform_admin") {
		return true
	}
	if dataset.Visibility == "public" {
		return true
	}
	if dataset.Visibility != "tenant" || dataset.OwnerTenant != identity.TenantID {
		return false
	}
	for _, role := range dataset.AllowedRoles {
		if identity.HasRole(role) {
			return true
		}
	}
	return false
}
