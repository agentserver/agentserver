package server

// This file holds package-level request/response types for the
// public REST API. swaggo annotations on handler funcs reference
// these by name; inline `var req struct {...}` shapes can't be
// referenced from annotations, which is why we extract them here.
//
// Group additions by API tag (Auth, Workspaces, …). Add new types
// alphabetically within each group so PRs from different tags don't
// trip over each other.

// --- Auth ---

// AuthCredentials is the email+password body for POST /api/auth/login
// and POST /api/auth/register.
type AuthCredentials struct {
	Email    string `json:"email" example:"alice@example.com"`
	Password string `json:"password" example:"hunter2"`
}

// AuthStatusResponse is the {"status":"ok"} envelope returned by
// /api/auth/login, /api/auth/logout, and /api/auth/check on success.
type AuthStatusResponse struct {
	Status string `json:"status" example:"ok"`
}

// AuthRegisterResponse is what POST /api/auth/register returns on
// success: the new user's id and the email it was registered with.
type AuthRegisterResponse struct {
	ID    string `json:"id"    example:"7e7a4f6c-..."`
	Email string `json:"email" example:"alice@example.com"`
}

// AuthMeResponse is the current user payload returned by GET /api/auth/me.
// Name and Picture are populated from OIDC profile data when present
// (login via password leaves both empty).
type AuthMeResponse struct {
	ID      string  `json:"id"`
	Email   string  `json:"email"`
	Name    *string `json:"name,omitempty"`
	Picture *string `json:"picture,omitempty"`
	Role    string  `json:"role" example:"developer"`
}
