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

// 005 backfills each credential's provider from the models using it (by kind when unused)
// and a Vertex credential's project/location from its models' extra.
func TestMigrate_005BackfillsCredentialProvider(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := d.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	// Roll provider_credentials back to its pre-005 shape so the migration runs again.
	exec(`ALTER TABLE provider_credentials DROP CONSTRAINT credential_vertex_is_google,
		DROP COLUMN provider, DROP COLUMN api_base, DROP COLUMN vertex_project, DROP COLUMN vertex_location`)
	exec(`DELETE FROM schema_migrations WHERE version = 'migrations/005_credential_endpoint.sql'`)

	cred := func(name, kind string) (id string) {
		t.Helper()
		if err := d.Pool.QueryRow(ctx, `INSERT INTO provider_credentials (name, kind, nonce, ciphertext)
			VALUES ($1, $2, 'n', 'c') RETURNING id`, name, kind).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	model := func(name, provider, credID, extra string) {
		exec(`INSERT INTO models (name, provider, upstream_model, credential_id, extra) VALUES ($1, $2, 'u', $3, $4)`,
			name, provider, credID, extra)
	}
	mostly := cred("mostly-anthropic", "api_key")
	model("a1", "anthropic", mostly, `{}`)
	model("a2", "anthropic", mostly, `{}`)
	model("o1", "openai", mostly, `{}`)
	cred("unused-key", "api_key")
	cred("unused-sa", "gcp_service_account")
	vx := cred("vertex", "gcp_service_account")
	model("g1", "google", vx, `{"vertex_project":"p1","vertex_location":"europe-west4"}`)

	if err := d.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	want := map[string][3]string{
		"mostly-anthropic": {"anthropic", "", ""},
		"unused-key":       {"openai", "", ""},
		"unused-sa":        {"google", "", ""},
		"vertex":           {"google", "p1", "europe-west4"},
	}
	rows, _ := d.Pool.Query(ctx, `SELECT name, provider::text, vertex_project, vertex_location, api_base FROM provider_credentials`)
	defer rows.Close()
	n := 0
	for rows.Next() {
		var name, base string
		var got [3]string
		rows.Scan(&name, &got[0], &got[1], &got[2], &base)
		if got != want[name] || base != "" {
			t.Errorf("%s: got %v base=%q, want %v", name, got, base, want[name])
		}
		n++
	}
	if n != len(want) {
		t.Fatalf("rows=%d", n)
	}
}

// 007 moves an endpoint that lived on a credential's models onto the credential when they all
// agree on one, and clears the models' now-redundant copies. Conflicts and Vertex are left alone.
func TestMigrate_007MovesModelEndpointToCredential(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := d.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	cred := func(name, kind, provider, base string) (id string) {
		t.Helper()
		if err := d.Pool.QueryRow(ctx, `INSERT INTO provider_credentials (name, kind, provider, api_base, nonce, ciphertext)
			VALUES ($1, $2, $3, $4, 'n', 'c') RETURNING id`, name, kind, provider, base).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	model := func(name, provider, credID, base string) {
		exec(`INSERT INTO models (name, provider, upstream_model, credential_id, api_base) VALUES ($1, $2, 'u', $3, $4)`,
			name, provider, credID, base)
	}
	agree := cred("agree", "api_key", "openai", "")
	model("a1", "openai", agree, "http://10.0.0.5:8000/v1")
	model("a2", "openai", agree, "http://10.0.0.5:8000/v1")
	model("a3", "openai", agree, "")
	conflict := cred("conflict", "api_key", "openai", "")
	model("b1", "openai", conflict, "https://x.example.com/v1")
	model("b2", "openai", conflict, "https://y.example.com/v1")
	set := cred("set", "api_key", "openai", "https://proxy.example.com/v1")
	model("c1", "openai", set, "https://other.example.com/v1")
	model("c2", "openai", set, "https://proxy.example.com/v1")
	vx := cred("vertex", "gcp_service_account", "google", "")
	model("v1", "google", vx, "https://psc.example.com")

	exec(`DELETE FROM schema_migrations WHERE version = 'migrations/007_credential_api_base_backfill.sql'`)
	if err := d.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"agree": "http://10.0.0.5:8000/v1", "conflict": "", "set": "https://proxy.example.com/v1", "vertex": ""} {
		var got string
		d.Pool.QueryRow(ctx, `SELECT api_base FROM provider_credentials WHERE name = $1`, name).Scan(&got)
		if got != want {
			t.Errorf("credential %s: %q, want %q", name, got, want)
		}
	}
	for name, want := range map[string]string{"a1": "", "a2": "", "a3": "", "b1": "https://x.example.com/v1", "b2": "https://y.example.com/v1",
		"c1": "https://other.example.com/v1", "c2": "", "v1": "https://psc.example.com"} {
		var got string
		d.Pool.QueryRow(ctx, `SELECT api_base FROM models WHERE name = $1`, name).Scan(&got)
		if got != want {
			t.Errorf("model %s: %q, want %q", name, got, want)
		}
	}
}
