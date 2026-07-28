package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/auth"
	"github.com/dingpuyu/rag-evolution-lab/internal/datasetaccess"
)

type fakeApplicationStore struct {
	applications []datasetaccess.Application
	bindings     []datasetaccess.KnowledgeBinding
}

func (store *fakeApplicationStore) VisibleApplications(context.Context, auth.Identity) ([]datasetaccess.Application, error) {
	return store.applications, nil
}

func (store *fakeApplicationStore) CreateApplication(_ context.Context, identity auth.Identity, input datasetaccess.CreateApplication) (datasetaccess.Application, error) {
	application := datasetaccess.Application{ID: identity.TenantID + "-" + input.Slug, TenantID: identity.TenantID, Name: input.Name, Slug: input.Slug, Status: "active", CreatedBy: identity.Subject, CreatedAt: time.Now().UTC()}
	store.applications = append(store.applications, application)
	return application, nil
}

func (*fakeApplicationStore) Environments(context.Context, auth.Identity, string) ([]datasetaccess.Environment, error) {
	return []datasetaccess.Environment{{ID: "tenant_a-support-dev", ApplicationID: "tenant_a-support", Name: "dev", ConfigVersion: "v1", Status: "active"}}, nil
}

func (*fakeApplicationStore) CreateEnvironment(context.Context, auth.Identity, string, datasetaccess.CreateEnvironment) (datasetaccess.Environment, error) {
	return datasetaccess.Environment{}, nil
}

func (store *fakeApplicationStore) Bindings(context.Context, auth.Identity, string, string) ([]datasetaccess.KnowledgeBinding, error) {
	return store.bindings, nil
}

func (store *fakeApplicationStore) CreateBinding(_ context.Context, identity auth.Identity, appID string, input datasetaccess.CreateBinding) (datasetaccess.KnowledgeBinding, error) {
	binding := datasetaccess.KnowledgeBinding{ApplicationID: appID, EnvironmentID: input.EnvironmentID, DatasetID: input.DatasetID, Status: "active", CreatedBy: identity.Subject, Policy: input.Policy}
	store.bindings = append(store.bindings, binding)
	return binding, nil
}

func applicationRequest(identity auth.Identity, method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	return request.WithContext(context.WithValue(request.Context(), identityContextKey{}, identity))
}

func TestApplicationAPIRequiresAdminAndEnforcesDatasetAuthorization(t *testing.T) {
	store := &fakeApplicationStore{applications: []datasetaccess.Application{{ID: "tenant_a-support", TenantID: "tenant_a", Name: "Support", Slug: "support", Status: "active"}}}
	api := &ApplicationAPI{store: store, datasetStore: datasetaccess.Defaults()}

	viewer := httptest.NewRecorder()
	api.list(viewer, applicationRequest(auth.Identity{Subject: "viewer", TenantID: "tenant_a", Roles: []string{"viewer"}}, http.MethodGet, "/api/v1/apps", ""))
	if viewer.Code != http.StatusForbidden {
		t.Fatalf("viewer application list status=%d body=%s", viewer.Code, viewer.Body.String())
	}

	admin := auth.Identity{Subject: "admin", TenantID: "tenant_a", Roles: []string{"admin"}}
	listed := httptest.NewRecorder()
	api.list(listed, applicationRequest(admin, http.MethodGet, "/api/v1/apps", ""))
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), "tenant_a-support") {
		t.Fatalf("admin application list status=%d body=%s", listed.Code, listed.Body.String())
	}

	accepted := httptest.NewRecorder()
	api.createBinding(accepted, applicationRequest(admin, http.MethodPost, "/api/v1/apps/tenant_a-support/bindings", `{"environment_id":"tenant_a-support-dev","dataset_id":"tenant-a-operations","policy":{"top_k":6}}`))
	if accepted.Code != http.StatusCreated || !strings.Contains(accepted.Body.String(), `"dataset_id":"tenant-a-operations"`) {
		t.Fatalf("binding status=%d body=%s", accepted.Code, accepted.Body.String())
	}

	crossTenant := httptest.NewRecorder()
	api.createBinding(crossTenant, applicationRequest(auth.Identity{Subject: "bob", TenantID: "tenant_b", Roles: []string{"admin"}}, http.MethodPost, "/api/v1/apps/tenant_a-support/bindings", `{"environment_id":"tenant_a-support-dev","dataset_id":"tenant-a-operations"}`))
	if crossTenant.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant binding status=%d body=%s", crossTenant.Code, crossTenant.Body.String())
	}
}
