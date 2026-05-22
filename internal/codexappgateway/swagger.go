// Package codexappgateway hosts the codex-app-gateway HTTP API. swag-style
// comments in this file declare the OpenAPI document metadata; per-route
// annotations live on individual handler funcs.
//
//	@title          codex-app-gateway API
//	@version        0.1.0
//	@description    Public REST surface of codex-app-gateway: submit codex
//	@description    turns from external integrators via a workspace-scoped
//	@description    bearer API key. The WS endpoint at "/" is not part of
//	@description    this spec — it follows codex's --remote protocol.
//	@BasePath       /
//	@securityDefinitions.apikey  WorkspaceAPIKey
//	@in                          header
//	@name                        Authorization
//	@description                 Bearer prefix required. Mint via agentserver's POST /api/workspaces/{wid}/api-keys.
//	@securityDefinitions.apikey  InternalSecret
//	@in                          header
//	@name                        X-Internal-Secret
//	@description                 Pre-shared secret for in-cluster RPC. Not for public consumers.
package codexappgateway
