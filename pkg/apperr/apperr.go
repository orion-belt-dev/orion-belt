// Package apperr gives errors a kind, so the layer that answers a caller
// (the HTTP API, the CLI) can map them to a status and a safe message without
// knowing where they came from.
//
// The convention, documented in docs/ERROR_HANDLING.md:
//   - Create errors the caller can act on with a kind: apperr.NotFound("…"),
//     apperr.Conflict("…"). Their message is shown to the caller.
//   - Wrap everything else with fmt.Errorf("doing x: %w", err) as usual. An
//     error with no kind is Internal: it is logged in full and the caller
//     sees only "internal error".
//   - Sentinels such as database.ErrNotFound carry a kind, so they map
//     correctly however deeply they are wrapped.
package apperr

import (
	"errors"
	"net/http"
)

// Kind classifies an error by what the caller should do about it.
type Kind int

const (
	Internal        Kind = iota // a bug or a failure the caller cannot fix
	Invalid                     // the request is malformed or breaks a rule
	Unauthenticated             // no valid credentials
	Forbidden                   // credentials valid, action not allowed
	NotFound                    // the thing named does not exist
	Conflict                    // the request clashes with current state
	Unavailable                 // a dependency is down; retrying may work
)

var kindNames = map[Kind]string{
	Internal:        "internal",
	Invalid:         "invalid",
	Unauthenticated: "unauthenticated",
	Forbidden:       "forbidden",
	NotFound:        "not_found",
	Conflict:        "conflict",
	Unavailable:     "unavailable",
}

// String is the kind's stable name, sent to API callers as "code".
func (k Kind) String() string { return kindNames[k] }

// HTTPStatus is the status an API answers with for the kind.
func (k Kind) HTTPStatus() int {
	switch k {
	case Invalid:
		return http.StatusBadRequest
	case Unauthenticated:
		return http.StatusUnauthorized
	case Forbidden:
		return http.StatusForbidden
	case NotFound:
		return http.StatusNotFound
	case Conflict:
		return http.StatusConflict
	case Unavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// Error is an error with a kind and a message safe to show the caller.
type Error struct {
	Kind Kind
	Msg  string
	Err  error // the underlying cause, never shown to the caller
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Msg + ": " + e.Err.Error()
	}
	return e.Msg
}

func (e *Error) Unwrap() error { return e.Err }

// New returns an error of kind with a public message.
func New(kind Kind, msg string) *Error { return &Error{Kind: kind, Msg: msg} }

// Wrap returns an error of kind with a public message and a hidden cause.
func Wrap(kind Kind, msg string, err error) *Error { return &Error{Kind: kind, Msg: msg, Err: err} }

// Constructors for the common kinds.
func Invalidf(msg string) *Error   { return New(Invalid, msg) }
func NotFoundf(msg string) *Error  { return New(NotFound, msg) }
func Forbiddenf(msg string) *Error { return New(Forbidden, msg) }
func Conflictf(msg string) *Error  { return New(Conflict, msg) }

// KindOf returns the kind of the first kinded error in err's chain, or
// Internal when there is none.
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return Internal
}

// Message returns what the caller may see: the public message of the first
// kinded error in the chain, or "internal error".
func Message(err error) string {
	var e *Error
	if errors.As(err, &e) && e.Kind != Internal {
		return e.Msg
	}
	return "internal error"
}
