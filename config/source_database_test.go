package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ponder.local/ponder-go/schema"
)

func sourceDatabaseInput(t *testing.T, database string) *SourceConfiguration {
	t.Helper()
	properties := `"chains":{"mainnet":{"id":1,"rpc":"https://rpc.com"}},"contracts":{"a":{"chain":"mainnet","abi":[]}}`
	if database != "" {
		properties += `,"database":` + database
	}
	c, err := ParseSourceConfiguration([]byte("{" + properties + "}"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSourceBuildPre(t *testing.T) {
	// Vitest's environment helper distinguishes deleted entries and empty strings.
	for _, key := range []string{"DATABASE_URL", "DATABASE_PRIVATE_URL", "PRIVATE_DATABASE_URL"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	const url = "postgres://username@localhost:5432/database"
	const private = "postgres://username@localhost:5432/better_database"
	const missing = "Invalid database configuration: Either 'connectionString' or 'poolConfig' must be defined."
	const options = `{"max":100,"ssl":{"ca":"ca","cert":"cert","key":"key"},"idle_in_transaction_session_timeout":10,"keepAlive":true}`
	for _, tc := range []struct {
		title, database, publicURL, privateURL, directory, connection, errorMessage string
	}{
		{title: "buildPre() database uses pglite by default", directory: filepath.Join(".ponder", "pglite")},
		{title: "buildPre() database respects custom pglite path", database: `{"kind":"pglite","directory":"custom-pglite/directory"}`, directory: filepath.Join("custom-pglite", "directory")},
		{title: "buildPre() database uses pglite if specified even if DATABASE_URL env var present", database: `{"kind":"pglite"}`, publicURL: url, directory: filepath.Join(".ponder", "pglite")},
		{title: "buildPre() database uses postgres if DATABASE_URL env var present", publicURL: url, connection: url},
		{title: "buildPre() database uses postgres if DATABASE_PRIVATE_URL env var present", publicURL: url, privateURL: private, connection: private},
		{title: "buildPre() throws for postgres database with no connection string", database: `{"kind":"postgres"}`, errorMessage: missing},
		{title: "buildPre() database with postgres uses pool config", database: `{"kind":"postgres","connectionString":"` + url + `","poolConfig":` + options + `}`, connection: url},
	} {
		t.Run(tc.title, func(t *testing.T) {
			if tc.publicURL != "" {
				t.Setenv("DATABASE_URL", tc.publicURL)
			}
			if tc.privateURL != "" {
				t.Setenv("DATABASE_PRIVATE_URL", tc.privateURL)
			}
			input := sourceDatabaseInput(t, tc.database)
			check := func() {
				t.Helper()
				pre, err := BuildSourcePre(input, ".ponder")
				if tc.errorMessage != "" {
					if err == nil || err.Error() != tc.errorMessage {
						t.Fatalf("unexpected error %v", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				db := pre.DatabaseConfig
				if tc.directory != "" {
					if db.Kind != "pglite" || db.Options == nil || !strings.Contains(db.Options.DataDir, tc.directory) {
						t.Fatalf("unexpected PGlite config %v", db)
					}
					return
				}
				if db.Kind != "postgres" || db.PoolConfig["connectionString"] != tc.connection {
					t.Fatalf("unexpected PostgreSQL config %v", db)
				}
				if tc.database != "" {
					want := `{"kind":"postgres","poolConfig":{"connectionString":"` + url + `","idle_in_transaction_session_timeout":10,"keepAlive":true,"max":100,"ssl":{"ca":"ca","cert":"cert","key":"key"}}}`
					raw, err := json.Marshal(db)
					if err != nil || string(raw) != want {
						t.Fatalf("pool options %s, %v", raw, err)
					}
				}
			}
			check()
			if tc.database == "" && tc.publicURL == "" {
				t.Setenv("DATABASE_URL", "")
				t.Setenv("PRIVATE_DATABASE_URL", "")
				check()
			}
		})
	}
}

func TestSourcePostgresName(t *testing.T) {
	for _, env := range []string{"PGHOST", "PGPORT", "PGDATABASE"} {
		t.Setenv(env, "")
	}
	for _, tc := range []struct {
		title string
		pool  PostgresPoolConfig
		env   map[string]string
		want  string
	}{
		{"getDatabaseName() prioritizes connectionString values", PostgresPoolConfig{"connectionString": "postgres://testhost:5432/pg", "host": "otherhost", "port": 5666}, nil, "testhost:5432/pg"},
		{"getDatabaseName() uses env vars", nil, map[string]string{"PGHOST": "testhost", "PGPORT": "5435", "PGDATABASE": "testdb"}, "testhost:5435/testdb"},
		{"getDatabaseName() uses defaults when config/env missing", nil, nil, "localhost:5432/"},
		{"getDatabaseName() prioritizes config over env", PostgresPoolConfig{"host": "testhost", "port": 5433, "database": "test"}, map[string]string{"PGDATABASE": "otherdatabase"}, "testhost:5433/test"},
		{"getDatabaseName() prioritizes connection string over other config values", PostgresPoolConfig{"connectionString": "postgres://testhost:5435/database", "database": "test", "port": 5433, "host": "testhost"}, map[string]string{"PGDATABASE": "otherdatabase"}, "testhost:5435/database"},
		{"getDatabaseName() mixes defaults, env, and config", PostgresPoolConfig{"database": "test"}, map[string]string{"PGHOST": "testhost"}, "testhost:5432/test"},
	} {
		t.Run(tc.title, func(t *testing.T) {
			for key, v := range tc.env {
				t.Setenv(key, v)
			}
			name, err := tc.pool.DatabaseName()
			if err != nil || name != tc.want {
				t.Fatalf("got %q, %v, want %q", name, err, tc.want)
			}
		})
	}
}

func TestSourceDatabaseBuildIntegration(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:secret@host/ignored")
	t.Setenv("DATABASE_PRIVATE_URL", "")
	input := sourceDatabaseInput(t, `{"kind":"pglite","directory":"memory://"}`)
	built, err := BuildSourceConfig(input, SourceBuildOptions{})
	if err != nil || built.Pre.Ordering != schema.Multichain || built.Pre.DatabaseConfig.Options.DataDir != "memory://" {
		t.Fatalf("memory configuration: %v, %v", built, err)
	}
	t.Setenv("DATABASE_URL", "")
	input = sourceDatabaseInput(t, `{"kind":"postgres","poolConfig":{"host":"example","ssl":null,"max":null}}`)
	first, err := BuildSourcePre(input, ".ponder")
	if err != nil {
		t.Fatal(err)
	}
	pool := first.DatabaseConfig.PoolConfig
	if _, ok := pool["connectionString"]; ok || pool["max"] != 30 || pool["ssl"] != false {
		t.Fatal("unexpected pool-only defaults", pool)
	}
	pool["host"] = "changed"
	second, err := BuildSourcePre(input, ".ponder")
	if err != nil || second.DatabaseConfig.PoolConfig["host"] != "example" {
		t.Fatal("build mutated source", err)
	}
}

func TestSourcePostgresNameURL(t *testing.T) {
	for _, env := range []string{"PGHOST", "PGPORT", "PGDATABASE"} {
		t.Setenv(env, "")
	}
	for _, tc := range []struct{ url, want string }{
		{"postgres://user:secret@[::1]:5432/db%20name?sslpassword=secret", "[::1]:5432/db name"},
		{"postgres://u@/db", "localhost:5432/db"},
		{"postgres://%2Fvar%2Frun/db", "/var/run:5432/db"},
		{"postgres://%2Fvar%2Frun/db?host=over", "over:5432/%2Fvar%2Frun/db"},
		{"postgres://host/db%2Fname%24%25", "host:5432/db%2Fname%24%"},
		{"socket:/var%2Frun?db=my", "/var%2Frun:5432/my"},
		{"postgres://source/db?host=a&host=b&port=12", "b:12/db"},
		{"postgresql://h:00044/a/../b", "h:44/b"},
		{"/var/run testdb", "/var/run:5432/testdb"},
	} {
		got, err := (PostgresPoolConfig{"connectionString": tc.url}).DatabaseName()
		if err != nil || got != tc.want {
			t.Errorf("got %q, %v, want %q", got, err, tc.want)
		}
	}
	if name, err := (PostgresPoolConfig{"port": 0}).DatabaseName(); err != nil || name != "localhost:0/" {
		t.Fatalf("explicit port zero: %q, %v", name, err)
	}
	_, err := (PostgresPoolConfig{"connectionString": "postgres://user:secret@host:invalid/db"}).DatabaseName()
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("invalid URL did not produce a redacted error")
	}
}
