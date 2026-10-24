package database

import (
	"errors"

	"github.com/orion-belt-dev/orion-belt/pkg/apperr"
)

// The sentinels callers can act on carry an apperr kind, so the API maps
// them to the right status however deeply they are wrapped.
var (
	// ErrNotFound is returned when a record is not found
	ErrNotFound error = apperr.NotFoundf("record not found")

	// ErrAlreadyExists is returned when a record already exists
	ErrAlreadyExists error = apperr.Conflictf("record already exists")

	// ErrInvalidInput is returned when input is invalid
	ErrInvalidInput error = apperr.Invalidf("invalid input")

	// ErrUnsupportedDriver is returned when the database driver is not supported
	ErrUnsupportedDriver = errors.New("unsupported database driver")

	// ErrConnectionFailed is returned when database connection fails
	ErrConnectionFailed error = apperr.New(apperr.Unavailable, "database connection failed")

	// ErrMigrationFailed is returned when database migration fails
	ErrMigrationFailed = errors.New("database migration failed")

	// ErrPermissionDenied is returned when permission check fails
	ErrPermissionDenied error = apperr.Forbiddenf("permission denied")

	// ErrAlreadyInitialized is returned by CreateFirstUser when any user
	// account already exists.
	ErrAlreadyInitialized error = apperr.Conflictf("users already exist")
)
