// Package clierr defines the errors the CLI reports and the exit code each
// one maps to. Scripts and agents branch on the code, so kinds are stable:
// add new ones, never renumber.
package clierr

import (
	"errors"
	"fmt"
)

// Kind classifies a failure.
type Kind string

const (
	Internal    Kind = "error"
	Usage       Kind = "usage"
	NotSignedIn Kind = "not_signed_in"
	Permission  Kind = "permission_denied"
	NotFound    Kind = "not_found"
	RateLimited Kind = "rate_limited"
	Upstream    Kind = "upstream_error"
	// AlreadyExists is a create that found the thing already there.
	AlreadyExists Kind = "already_exists"
	ChecksFailed  Kind = "checks_failed"
	// Interrupted is a run the user stopped, with Ctrl-C.
	Interrupted Kind = "interrupted"
)

// ExitCode is the process exit status for a failure of this kind. 8 is kept
// free for "timed out waiting", which the commands that wait for an event
// will use.
func (k Kind) ExitCode() int {
	switch k {
	case Usage:
		return 2
	case NotSignedIn:
		return 3
	case Permission:
		return 4
	case NotFound:
		return 5
	case RateLimited:
		return 6
	case Upstream:
		return 7
	case AlreadyExists:
		return 10
	case ChecksFailed:
		return 9
	case Interrupted:
		// The shells' code for a program stopped by SIGINT.
		return 130
	}
	return 1
}

// Error is a failure with a kind, a message for the user and, where there is
// one, the next thing to try.
type Error struct {
	Kind    Kind
	Message string
	Hint    string
	Cause   error
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Cause }

// New builds an error of the given kind.
func New(kind Kind, format string, a ...any) *Error {
	return &Error{Kind: kind, Message: fmt.Sprintf(format, a...)}
}

// Wrap builds an error of the given kind that keeps cause for errors.Is/As.
func Wrap(kind Kind, cause error, format string, a ...any) *Error {
	return &Error{Kind: kind, Message: fmt.Sprintf(format, a...), Cause: cause}
}

// WithHint sets the suggested next step and returns the error.
func (e *Error) WithHint(format string, a ...any) *Error {
	e.Hint = fmt.Sprintf(format, a...)
	return e
}

// From returns err as an *Error, treating anything unclassified as Internal.
func From(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Kind: Internal, Message: err.Error(), Cause: err}
}
