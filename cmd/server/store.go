package main

import (
	"context"
	"fmt"

	"github.com/orion-belt-dev/orion-belt/pkg/database"
)

// openStore connects to the configured database and runs Migrate/repair so CLI
// mutations see the same schema the gateway would after startup.
func openStore(ctx context.Context) (database.Store, error) {
	config, err := loadConfig()
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	store, err := database.NewStore(config.Database.Driver, config.Database.ConnectionString)
	if err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}
	if err := store.Connect(ctx); err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("connect: %w", err)
	}
	if err := store.Migrate(ctx); err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("migrate/repair: %w", err)
	}
	return store, nil
}
