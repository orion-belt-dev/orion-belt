package database

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

// postgresImage matches the image in docker-compose.server.yml, pinned by
// digest so the integration tests run against a fixed build.
const postgresImage = "postgres:16-alpine@sha256:721873c34ceb9f8d8fc265984940dc982404c105f19ad51be9fdc5970a6080ea"

// TestMain starts a throwaway Postgres container for the integration tests
// unless ORION_TEST_POSTGRES_DSN already points at a database. Without
// Docker the DSN stays unset and those tests skip, as they did before.
func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(run(m))
}

func run(m *testing.M) int {
	if os.Getenv("ORION_TEST_POSTGRES_DSN") != "" || testing.Short() {
		return m.Run()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	container, err := tcpostgres.Run(ctx, postgresImage,
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
	os.Setenv("ORION_TEST_POSTGRES_DSN", dsn)
	return m.Run()
}
