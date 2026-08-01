package coreserver

import (
	"errors"
	"net/http"
	"strings"
)

// InternalApprovalActionHandler owns the one method-qualified wildcard route
// shared by observe, consume, cancel, and expire. Registering the observation
// handler directly on that route would make Go's ServeMux prefer it over the
// less-specific approval prefix for every POST action.
type InternalApprovalActionHandler struct {
	commands    http.Handler
	observation http.Handler
}

func NewInternalApprovalActionHandler(commands, observation http.Handler) (*InternalApprovalActionHandler, error) {
	if commands == nil || observation == nil {
		return nil, errors.New("approval command and observation handlers are required")
	}
	return &InternalApprovalActionHandler{commands: commands, observation: observation}, nil
}

func (handler *InternalApprovalActionHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	action := request.PathValue("approvalAction")
	if _, observe := strings.CutSuffix(action, ":observe"); observe {
		handler.observation.ServeHTTP(response, request)
		return
	}
	handler.commands.ServeHTTP(response, request)
}

var _ http.Handler = (*InternalApprovalActionHandler)(nil)
