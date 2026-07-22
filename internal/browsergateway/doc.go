// Package browsergateway exposes the codex harness as a standard AG-UI
// (https://github.com/ag-ui-protocol/ag-ui) agent endpoint over HTTP+SSE.
// It is a ws client of codex-app-gateway's /codex-app/ws and translates
// codex v2 notification frames into AG-UI events.
package browsergateway
