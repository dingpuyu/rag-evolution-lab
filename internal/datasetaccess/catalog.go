package datasetaccess

import (
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
		{ID: "public-identity", Name: "身份与单点登录", Description: "公开的 SSO、SAML 与 OIDC 产品文档", Product: "identity", Visibility: "public"},
		{ID: "public-reports", Name: "报表中心", Description: "公开的报表导出与权限说明", Product: "reports", Visibility: "public"},
		{ID: "tenant-a-operations", Name: "Tenant A 运维知识库", Description: "Tenant A 管理员专属运行手册", Product: "tenant-operations", Visibility: "tenant", OwnerTenant: "tenant_a", AllowedRoles: []string{"admin"}},
		{ID: "tenant-b-operations", Name: "Tenant B 运维知识库", Description: "Tenant B 管理员专属运行手册", Product: "tenant-operations", Visibility: "tenant", OwnerTenant: "tenant_b", AllowedRoles: []string{"admin"}},
	})
}

func (catalog *Catalog) Visible(identity auth.Identity) []Dataset {
	result := make([]Dataset, 0, len(catalog.byID))
	for _, dataset := range catalog.byID {
		if catalog.allowed(dataset, identity) {
			result = append(result, dataset)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (catalog *Catalog) Authorize(id string, identity auth.Identity) (Dataset, error) {
	dataset, ok := catalog.byID[strings.TrimSpace(id)]
	if !ok {
		return Dataset{}, ErrDatasetNotFound
	}
	if !catalog.allowed(dataset, identity) {
		return Dataset{}, ErrDatasetDenied
	}
	return dataset, nil
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
