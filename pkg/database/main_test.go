package database

import (
	"os"
	"testing"

	"github.com/orion-belt-dev/orion-belt/internal/pgtest"
)

// TestMain points the integration tests at a throwaway Postgres container
// unless ORION_TEST_POSTGRES_DSN already names a database (see pgtest.Main).
func TestMain(m *testing.M) {
	os.Exit(pgtest.Main(m))
}
