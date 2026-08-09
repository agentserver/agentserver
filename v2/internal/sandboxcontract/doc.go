// Package sandboxcontract defines the versioned internal contract exposed by
// sandbox-gateway. The profile is E2B-shaped at the semantic level, but it is
// intentionally not compatible with the official E2B SDK or HTTP wire format.
// Provider SDK types and provider session identifiers are not part of this
// contract; operation/request handles may appear only as bounded opaque values.
package sandboxcontract
