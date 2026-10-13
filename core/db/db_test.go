package db_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"tollgate/core/db"
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

// serve and `admin create` both migrate at startup; on a fresh database they must not collide.
func TestMigrate_ConcurrentRunners(t *testing.T) {
	url := os.Getenv("TOLLGATE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TOLLGATE_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	schema := fmt.Sprintf("t_mig_%d", time.Now().UnixNano())
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")

	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		go func() {
			cfg, _ := pgxpool.ParseConfig(url)
			cfg.ConnConfig.RuntimeParams["search_path"] = schema
			pool, err := pgxpool.NewWithConfig(ctx, cfg)
			if err != nil {
				errs <- err
				return
			}
			defer pool.Close()
			errs <- (&db.DB{Pool: pool}).Migrate(ctx)
		}()
	}
	for i := 0; i < 4; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent migrate: %v", err)
		}
	}
}
