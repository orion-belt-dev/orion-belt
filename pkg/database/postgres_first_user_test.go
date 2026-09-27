package database

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

// openTestPostgres connects to the database named by ORION_TEST_POSTGRES_DSN,
// migrates it and empties the users table. The test is skipped when the
// variable is unset, e.g.:
//
//	docker run -d --rm -p 55432:5432 -e POSTGRES_PASSWORD=test postgres:16-alpine
//	ORION_TEST_POSTGRES_DSN='postgres://postgres:test@127.0.0.1:55432/postgres?sslmode=disable' go test ./pkg/database/
func openTestPostgres(t *testing.T) *PostgresStore {
	t.Helper()
	dsn := os.Getenv("ORION_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("ORION_TEST_POSTGRES_DSN not set")
	}
	store, err := NewPostgresStore(dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	pg := store.(*PostgresStore)
	t.Cleanup(func() { _ = pg.Close() })

	ctx := context.Background()
	if err := pg.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := pg.db.ExecContext(ctx, `TRUNCATE users CASCADE`); err != nil {
		t.Fatalf("truncate users: %v", err)
	}
	return pg
}

func TestCreateFirstUserConcurrentCallersCreateOne(t *testing.T) {
	pg := openTestPostgres(t)
	ctx := context.Background()

	const n = 20
	// Keep n connections open so every caller starts its transaction at
	// once instead of being staggered by connection setup; otherwise the
	// race this guards against rarely materialises.
	pg.db.SetMaxIdleConns(n)
	var warm sync.WaitGroup
	for i := 0; i < n; i++ {
		warm.Add(1)
		go func() { defer warm.Done(); _ = pg.db.PingContext(ctx) }()
	}
	warm.Wait()

	for round := 0; round < 20; round++ {
		if _, err := pg.db.ExecContext(ctx, `TRUNCATE users CASCADE`); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		errs := make([]error, n)
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				u := common.NewUser(fmt.Sprintf("admin-%d-%d", round, i), fmt.Sprintf("a%d@example.com", i), "", true)
				<-start
				errs[i] = pg.CreateFirstUser(ctx, u)
			}(i)
		}
		close(start)
		wg.Wait()

		created := 0
		for _, err := range errs {
			switch {
			case err == nil:
				created++
			case errors.Is(err, ErrAlreadyInitialized):
			default:
				t.Fatalf("round %d: unexpected error: %v", round, err)
			}
		}
		users, err := pg.ListUsers(ctx, 100, 0)
		if err != nil {
			t.Fatal(err)
		}
		if created != 1 || len(users) != 1 {
			t.Fatalf("round %d: created=%d users=%d, want exactly one first user", round, created, len(users))
		}
	}
}

func TestCreateFirstUserRefusesWhenUsersExist(t *testing.T) {
	pg := openTestPostgres(t)
	ctx := context.Background()

	if err := pg.CreateUser(ctx, common.NewUser("existing", "e@example.com", "", false)); err != nil {
		t.Fatal(err)
	}
	err := pg.CreateFirstUser(ctx, common.NewUser("late-admin", "l@example.com", "", true))
	if !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("err = %v, want ErrAlreadyInitialized", err)
	}
}
