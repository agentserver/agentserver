package executionbackend

import "context"

// Backend dispatches already-authorized operations to exactly one provider
// kind. Implementations must not select a different target or generation.
type Backend interface {
	Kind() Kind
	StartProcess(context.Context, StartProcessRequest) (Exchange, error)
	SignalProcess(context.Context, SignalProcessRequest) (Exchange, error)
	ReadFile(context.Context, ReadFileRequest) (Exchange, error)
}

// Exchange represents one provider operation. Implementations cache the
// acknowledgement and terminal result so reconciliation may observe them more
// than once. NextEvent returns events in strictly increasing Sequence order and
// io.EOF after the retained event stream is exhausted.
type Exchange interface {
	Target() Target
	Operation() OperationContext
	AwaitAcknowledgement(context.Context) (Acknowledgement, error)
	NextEvent(context.Context) (Event, error)
	AwaitTerminal(context.Context) (TerminalResult, error)
	Done() <-chan struct{}
}
