// Package server hosts agentserver's HTTP API. The swag-style comments
// in this file declare the OpenAPI document metadata; per-route
// annotations live on individual handler funcs.
//
//	@title          agentserver API
//	@version        0.1.0
//	@description    Public REST API for agentserver. Generated from
//	@description    Go handler annotations via swaggo/swag; see
//	@description    docs/api/README.md for regeneration instructions.
//	@BasePath       /
//	@securityDefinitions.apikey  CookieAuth
//	@in                          cookie
//	@name                        agentserver-token
//	@description                 Session cookie set by POST /api/auth/login. All non-auth endpoints assume this cookie.
package server
