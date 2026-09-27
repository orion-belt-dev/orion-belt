package database

import "errors"

var (
	// ErrNotFound is returned when a record is not found
	ErrNotFound = errors.New("record not found")

	// ErrAlreadyExists is returned when a record already exists
	ErrAlreadyExists = errors.New("record already exists")

	// ErrInvalidInput is returned when input is invalid
	ErrInvalidInput = errors.New("invalid input")

	// ErrUnsupportedDriver is returned when the database driver is not supported
	ErrUnsupportedDriver = errors.New("unsupported database driver")

	// ErrConnectionFailed is returned when database connection fails
	ErrConnectionFailed = errors.New("database connection failed")

	// ErrMigrationFailed is returned when database migration fails
	ErrMigrationFailed = errors.New("database migration failed")

	// ErrPermissionDenied is returned when permission check fails
	ErrPermissionDenied = errors.New("permission denied")

	// ErrAlreadyInitialized is returned by CreateFirstUser when any user
	// account already exists.
	ErrAlreadyInitialized = errors.New("users already exist")
)
