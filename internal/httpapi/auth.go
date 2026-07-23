package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/auth"
)

type identityContextKey struct{}

type EnterpriseOptions struct {
	Verifier  auth.Verifier
	DevIssuer *auth.Manager
	Audit     *auth.AuditLog
}

type authAPI struct {
	verifier  auth.Verifier
	devIssuer *auth.Manager
	audit     *auth.AuditLog
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

func (api *authAPI) requireIdentity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		requestID := newRequestID()
		writer.Header().Set("X-Request-ID", requestID)
		writer.Header().Set("Cache-Control", "no-store")
		identity, err := api.verifier.VerifyAuthorization(request.Header.Get("Authorization"))
		if err != nil {
			writeError(writer, http.StatusUnauthorized, "authentication_required", "a valid Bearer token is required")
			api.audit.Append(auth.AuditEvent{
				RequestID: requestID, Timestamp: time.Now().UTC(), Method: request.Method, Path: request.URL.Path,
				Decision: "denied", Reason: err.Error(), Status: http.StatusUnauthorized, DurationMS: elapsedMS(started),
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
		"platform_admin":   {Subject: "demo-platform-admin", TenantID: "platform", Roles: []string{"platform_admin"}},
	}
	identity, ok := personas[input.Persona]
	if !ok {
		writeError(writer, http.StatusBadRequest, "unknown_persona", "persona must be one of the server-defined demo identities")
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
