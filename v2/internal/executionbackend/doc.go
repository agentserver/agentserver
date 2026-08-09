// Package executionbackend defines the provider-neutral dispatch boundary used
// by the execution gateway. Provider SDK and wire types must not cross this
// package boundary.
//
// A successful Backend method call only establishes an Exchange. It does not
// mean the provider accepted the operation: callers must wait for
// Exchange.AwaitAcknowledgement before committing the Core acknowledgement.
// Once Core has granted BeginOperationDispatch, an error is retry evidence only
// when it is a DispatchError with OutcomeNotSent.
package executionbackend
