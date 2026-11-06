// Package auth implements authentication: password hashing, tokens, refresh token
// families and API keys.
package auth

import "github.com/google/uuid"

const (
	RoleUser  = "user"
	RoleAdmin = "admin"
)

// Principal is the authenticated caller, as established by the auth middleware.
type Principal struct {
	UserID uuid.UUID
	Role   string
	// Via records the credential type: "jwt" or "api_key".
	Via string
}
