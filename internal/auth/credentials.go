package auth

import "context"

// CredentialVerifier is intentionally separate from JWT verification: service
// credentials are opaque, revocable secrets and never need to be decoded by
// the HTTP layer.
type CredentialVerifier interface {
	VerifyApplicationCredential(context.Context, string) (Identity, error)
}
