package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/agent"
	"github.com/dingpuyu/rag-evolution-lab/internal/auth"
	"github.com/dingpuyu/rag-evolution-lab/internal/cost"
	"github.com/dingpuyu/rag-evolution-lab/internal/datasetaccess"
	"github.com/dingpuyu/rag-evolution-lab/internal/generation"
	"github.com/dingpuyu/rag-evolution-lab/internal/indexbuild"
	"github.com/dingpuyu/rag-evolution-lab/internal/ingestionjob"
	"github.com/dingpuyu/rag-evolution-lab/internal/querytrace"
	"github.com/dingpuyu/rag-evolution-lab/internal/ratelimit"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type identityContextKey struct{}

type EnterpriseOptions struct {
	Verifier            auth.Verifier
	IdentityProvisioner auth.IdentityProvisioner
	CredentialVerifier  auth.CredentialVerifier
	CredentialStore     datasetaccess.CredentialStore
	DevIssuer           *auth.Manager
	LocalAccounts       *auth.AccountStore
	Audit               *auth.AuditLog
	IngestionJobs       *ingestionjob.Service
	DatasetStore        datasetaccess.Store
	ApplicationStore    datasetaccess.ApplicationStore
	IndexStore          datasetaccess.IndexStore
	QueryTraceStore     querytrace.Store
	IndexBuilds         *indexbuild.Service
	Generator           generation.Generator
	AgentPlanner        agent.Planner
	Tracer              trace.Tracer
	Cost                *cost.Calculator
	Limiter             ratelimit.Gate
}

type authAPI struct {
	verifier    auth.Verifier
	provisioner auth.IdentityProvisioner
	credentials auth.CredentialVerifier
	devIssuer   *auth.Manager
	accounts    *auth.AccountStore
	audit       *auth.AuditLog
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (recorder *statusRecorder) WriteHeader(status int) {
	recorder.status = status
	recorder.ResponseWriter.WriteHeader(status)
}

func (recorder *statusRecorder) Write(data []byte) (int, error) {
	if recorder.status == 0 {
		recorder.WriteHeader(http.StatusOK)
	}
	return recorder.ResponseWriter.Write(data)
}

func (recorder *statusRecorder) Flush() {
	if recorder.status == 0 {
		recorder.WriteHeader(http.StatusOK)
	}
	if flusher, ok := recorder.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (api *authAPI) requireIdentity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		request = request.WithContext(otel.GetTextMapPropagator().Extract(request.Context(), propagation.HeaderCarrier(request.Header)))
		started := time.Now()
		requestID := newRequestID()
		writer.Header().Set("X-Request-ID", requestID)
		writer.Header().Set("Cache-Control", "no-store")
		authorization := strings.TrimSpace(request.Header.Get("Authorization"))
		var identity auth.Identity
		var err error
		scheme, credentialValue, hasScheme := strings.Cut(authorization, " ")
		if hasScheme && strings.EqualFold(scheme, "AppCredential") && api.credentials != nil {
			identity, err = api.credentials.VerifyApplicationCredential(request.Context(), strings.TrimSpace(credentialValue))
		} else {
			identity, err = api.verifier.VerifyAuthorization(authorization)
		}
		if err != nil {
			writeError(writer, http.StatusUnauthorized, "authentication_required", "a valid Bearer token is required")
			api.audit.Append(auth.AuditEvent{
				RequestID: requestID, Timestamp: time.Now().UTC(), Method: request.Method, Path: request.URL.Path,
				Decision: "denied", Reason: err.Error(), Status: http.StatusUnauthorized, DurationMS: elapsedMS(started),
			})
			return
		}
		if identity.ApplicationID != "" && !applicationCredentialPathAllowed(request.URL.Path, identity.ApplicationID) {
			writeError(writer, http.StatusForbidden, "credential_scope_violation", "application credential cannot access this resource")
			api.audit.Append(auth.AuditEvent{
				RequestID: requestID, Timestamp: time.Now().UTC(), Subject: identity.Subject,
				TenantID: identity.TenantID, Roles: append([]string(nil), identity.Roles...),
				Method: request.Method, Path: request.URL.Path, Decision: "denied",
				Reason: "application credential path scope violation", Status: http.StatusForbidden,
				DurationMS: elapsedMS(started),
			})
			return
		}
		recorder := &statusRecorder{ResponseWriter: writer}
		next.ServeHTTP(recorder, request.WithContext(context.WithValue(request.Context(), identityContextKey{}, identity)))
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		decision := "allowed"
		if status >= 400 {
			decision = "denied"
		}
		api.audit.Append(auth.AuditEvent{
			RequestID: requestID, Timestamp: time.Now().UTC(), Subject: identity.Subject,
			TenantID: identity.TenantID, Roles: append([]string(nil), identity.Roles...),
			Method: request.Method, Path: request.URL.Path, Decision: decision, Status: status, DurationMS: elapsedMS(started),
		})
	})
}

func applicationCredentialPathAllowed(path, applicationID string) bool {
	prefix := "/api/v1/apps/" + strings.TrimSpace(applicationID) + "/"
	return strings.HasPrefix(path, prefix)
}

func (api *authAPI) devToken(writer http.ResponseWriter, request *http.Request) {
	if api.devIssuer == nil {
		http.NotFound(writer, request)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Pragma", "no-cache")
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input struct {
		Persona string `json:"persona"`
	}
	if err := decoder.Decode(&input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := ensureEOF(decoder); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	personas := map[string]auth.Identity{
		"public_viewer":    {Subject: "demo-public", TenantID: "public", Roles: []string{"viewer"}},
		"tenant037_viewer": {Subject: "demo-viewer-037", TenantID: "tenant_037", Roles: []string{"viewer"}},
		"tenant037_admin":  {Subject: "demo-admin-037", TenantID: "tenant_037", Roles: []string{"admin"}},
		"tenant_a_admin":   {Subject: "demo-admin-a", TenantID: "tenant_a", Roles: []string{"admin"}},
		"tenant_b_admin":   {Subject: "demo-admin-b", TenantID: "tenant_b", Roles: []string{"admin"}},
		"platform_admin":   {Subject: "demo-platform-admin", TenantID: "platform", Roles: []string{"platform_admin"}},
	}
	identity, ok := personas[input.Persona]
	if !ok {
		writeError(writer, http.StatusBadRequest, "unknown_persona", "persona must be one of the server-defined demo identities")
		return
	}
	if err := api.provisionIdentity(request.Context(), identity); err != nil {
		writeError(writer, http.StatusServiceUnavailable, "identity_provisioning_failed", err.Error())
		return
	}
	token, err := api.devIssuer.Issue(identity)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "token_issue_failed", err.Error())
		return
	}
	verified, err := api.devIssuer.VerifyAuthorization("Bearer " + token)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "token_verify_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"access_token": token, "token_type": "Bearer", "expires_at": verified.Expires, "identity": verified,
	})
}

func (api *authAPI) register(writer http.ResponseWriter, request *http.Request) {
	if api.accounts == nil || api.devIssuer == nil {
		http.NotFound(writer, request)
		return
	}
	var input struct {
		Email        string `json:"email"`
		Password     string `json:"password"`
		Organization string `json:"organization"`
	}
	if !decodeAuthInput(writer, request, &input) {
		return
	}
	identity, err := api.accounts.Register(auth.Registration{
		Email: input.Email, Password: input.Password, Organization: input.Organization,
	})
	if err != nil {
		status, code := http.StatusUnprocessableEntity, "registration_failed"
		if errors.Is(err, auth.ErrAccountExists) {
			status, code = http.StatusConflict, "account_exists"
		}
		writeError(writer, status, code, err.Error())
		return
	}
	if err := api.provisionIdentity(request.Context(), identity); err != nil {
		writeError(writer, http.StatusServiceUnavailable, "identity_provisioning_failed", err.Error())
		return
	}
	api.issueIdentity(writer, identity, http.StatusCreated)
}

func (api *authAPI) login(writer http.ResponseWriter, request *http.Request) {
	if api.accounts == nil || api.devIssuer == nil {
		http.NotFound(writer, request)
		return
	}
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeAuthInput(writer, request, &input) {
		return
	}
	identity, err := api.accounts.Authenticate(input.Email, input.Password)
	if err != nil {
		writeError(writer, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	}
	if err := api.provisionIdentity(request.Context(), identity); err != nil {
		writeError(writer, http.StatusServiceUnavailable, "identity_provisioning_failed", err.Error())
		return
	}
	api.issueIdentity(writer, identity, http.StatusOK)
}

func (api *authAPI) provisionIdentity(ctx context.Context, identity auth.Identity) error {
	if api.provisioner == nil {
		return nil
	}
	return api.provisioner.ProvisionIdentity(ctx, identity)
}

func (api *authAPI) issueIdentity(writer http.ResponseWriter, identity auth.Identity, status int) {
	token, err := api.devIssuer.Issue(identity)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "token_issue_failed", err.Error())
		return
	}
	verified, err := api.devIssuer.VerifyAuthorization("Bearer " + token)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "token_verify_failed", err.Error())
		return
	}
	writeJSON(writer, status, map[string]any{
		"access_token": token, "token_type": "Bearer", "expires_at": verified.Expires, "identity": verified,
	})
}

func decodeAuthInput(writer http.ResponseWriter, request *http.Request, destination any) bool {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Pragma", "no-cache")
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return false
	}
	if err := ensureEOF(decoder); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return false
	}
	return true
}

func (api *authAPI) me(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, identityFromContext(request.Context()))
}

func (api *authAPI) recentAudit(writer http.ResponseWriter, request *http.Request) {
	identity := identityFromContext(request.Context())
	if !identity.HasRole("platform_admin") {
		writeError(writer, http.StatusForbidden, "forbidden", "audit log requires platform_admin")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"events": api.audit.Recent(30)})
}

func identityFromContext(ctx context.Context) auth.Identity {
	identity, _ := ctx.Value(identityContextKey{}).(auth.Identity)
	return identity
}

func newRequestID() string {
	var bytes [12]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "request-id-unavailable"
	}
	return hex.EncodeToString(bytes[:])
}

func elapsedMS(started time.Time) float64 {
	return float64(time.Since(started).Microseconds()) / 1000
}
