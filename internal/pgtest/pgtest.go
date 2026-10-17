// Package pgtest starts a throwaway Postgres for integration tests. It is
// imported only from _test.go files, so the container tooling never reaches a
// shipped binary.
package pgtest

import (
	"context"
	"flag"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// EnvDSN names the variable integration tests read their database from.
const EnvDSN = "ORION_TEST_POSTGRES_DSN"

// image matches the image in docker-compose.server.yml, pinned by digest so
// the integration tests run against a fixed build.
const image = "postgres:16-alpine@sha256:721873c34ceb9f8d8fc265984940dc982404c105f19ad51be9fdc5970a6080ea"

// Main runs a package's tests with EnvDSN pointing at a database. An existing
// EnvDSN is used as is; otherwise a container is started for the run and
// removed afterwards. Without Docker, or with -short, EnvDSN stays unset and
// tests that need it skip. Call it from TestMain: os.Exit(pgtest.Main(m)).
func Main(m *testing.M) int {
	flag.Parse()
	if os.Getenv(EnvDSN) != "" || testing.Short() {
		return m.Run()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	container, err := tcpostgres.Run(ctx, image,
		tcpostgres.WithDatabase("orion"),
		tcpostgres.WithUsername("orion"),
		tcpostgres.WithPassword("orion"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "postgres container unavailable, integration tests will skip: %v\n", err)
		return m.Run()
	}
	defer func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			fmt.Fprintf(os.Stderr, "terminate postgres container: %v\n", err)
		}
	}()

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "postgres container DSN: %v\n", err)
		return 1
	}
	os.Setenv(EnvDSN, dsn)
	return m.Run()
}

// DSN returns the database for this test run, skipping t when there is none.
func DSN(t testing.TB) string {
	t.Helper()
	dsn := os.Getenv(EnvDSN)
	if dsn == "" {
		t.Skip(EnvDSN + " not set and no container available")
	}
	return dsn
}
