package server

// This file holds package-level request/response types for the
// public REST API. swaggo annotations on handler funcs reference
// these by name; inline `var req struct {...}` shapes can't be
// referenced from annotations, which is why we extract them here.
//
// Group additions by API tag (Auth, Workspaces, …). Add new types
// alphabetically within each group so PRs from different tags don't
// trip over each other.
//
// IMPORTANT: required JSON fields need `validate:"required"` so swag
// emits them in the OpenAPI schema's `required` array. Without it the
// frontend codegen treats every field as `T | undefined`.

// --- Auth ---

// AuthCredentials is the email+password body for POST /api/auth/login
// and POST /api/auth/register.
type AuthCredentials struct {
	Email    string `json:"email" example:"alice@example.com" validate:"required"`
	Password string `json:"password" example:"hunter2" validate:"required"`
} //@name AuthCredentials

// AuthStatusResponse is the {"status":"ok"} envelope returned by
// /api/auth/login, /api/auth/logout, and /api/auth/check on success.
type AuthStatusResponse struct {
	Status string `json:"status" example:"ok" validate:"required"`
} //@name AuthStatusResponse

// AuthRegisterResponse is what POST /api/auth/register returns on
// success: the new user's id and the email it was registered with.
type AuthRegisterResponse struct {
	ID    string `json:"id"    example:"7e7a4f6c-..." validate:"required"`
	Email string `json:"email" example:"alice@example.com" validate:"required"`
} //@name AuthRegisterResponse

// AuthMeResponse is the current user payload returned by GET /api/auth/me.
// Name and Picture are populated from OIDC profile data when present
// (login via password leaves both empty). Both fields are always present
// in the JSON response — nil pointers serialize as null (not omitted).
type AuthMeResponse struct {
	ID      string  `json:"id" validate:"required"`
	Email   string  `json:"email" validate:"required"`
	Name    *string `json:"name" extensions:"x-nullable=true"`
	Picture *string `json:"picture" extensions:"x-nullable=true"`
	Role    string  `json:"role" example:"developer" validate:"required"`
} //@name AuthMeResponse
