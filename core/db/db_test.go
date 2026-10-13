package db_test

import (
	"context"
	"testing"

	"tollgate/core/db/dbtest"
)

func TestMigrate_IdempotentAndSeeds(t *testing.T) {
	d := dbtest.New(t) // already migrated once
	ctx := context.Background()
	if err := d.Migrate(ctx); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	var groups, settings int
	d.Pool.QueryRow(ctx, "SELECT count(*) FROM groups WHERE name='default'").Scan(&groups)
	d.Pool.QueryRow(ctx, "SELECT count(*) FROM server_settings").Scan(&settings)
	if groups != 1 || settings != 1 {
		t.Fatalf("groups=%d settings=%d", groups, settings)
	}
}
