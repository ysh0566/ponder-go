package config

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ysh0566/ponder-go/schema"
)

// SourcePreBuild is the resolved database configuration and indexing order available
// before chain or indexing-function compilation. Resolving it performs no database I/O.
type SourcePreBuild struct {
	DatabaseConfig SourceDatabaseConfig `json:"databaseConfig"`
	Ordering       schema.Ordering      `json:"ordering"`
}

type SourceDatabaseConfig struct {
	Kind       string             `json:"kind"`
	Options    *PGliteOptions     `json:"options,omitempty"`
	PoolConfig PostgresPoolConfig `json:"poolConfig,omitempty"`
}

type PGliteOptions struct {
	DataDir string `json:"dataDir"`
}

// PostgresPoolConfig retains the source driver's pool and TLS options.
type PostgresPoolConfig map[string]any

func BuildSourcePre(config *SourceConfiguration, ponderDir string) (*SourcePreBuild, error) {
	if config == nil || config.value == nil {
		return nil, fmt.Errorf("source configuration is required")
	}
	ordering, _ := config.value.get("ordering").(string)
	result := &SourcePreBuild{Ordering: schema.Ordering(cmp.Or(ordering, string(schema.Multichain)))}
	input, _ := config.value.get("database").(*sourceObject)
	kind, directory, connection := "", "", ""
	var pool *sourceObject
	if input != nil {
		kind, _ = input.get("kind").(string)
		switch kind {
		case "pglite":
			directory, _ = input.get("directory").(string)
		case "postgres":
			connection, _ = input.get("connectionString").(string)
			pool, _ = input.get("poolConfig").(*sourceObject)
		}
	}
	connection = cmp.Or(connection, os.Getenv("DATABASE_PRIVATE_URL"), os.Getenv("DATABASE_URL"))
	switch {
	case kind == "postgres", kind == "" && connection != "":
		if connection == "" && pool == nil {
			return nil, fmt.Errorf("Invalid database configuration: Either 'connectionString' or 'poolConfig' must be defined.")
		}
		resolved := PostgresPoolConfig{}
		if connection != "" {
			resolved["connectionString"] = connection
		}
		if pool != nil {
			for _, key := range pool.keys {
				resolved[key] = sourcePlain(pool.get(key))
			}
		}
		if sourceNullish(resolved["max"]) {
			resolved["max"] = 30
		}
		if kind == "postgres" && sourceNullish(resolved["ssl"]) {
			resolved["ssl"] = false
		}
		result.DatabaseConfig = SourceDatabaseConfig{Kind: "postgres", PoolConfig: resolved}
	case kind == "pglite", kind == "":
		switch {
		case directory == "":
			directory = filepath.Join(cmp.Or(ponderDir, ".ponder"), "pglite")
		case directory != "memory://":
			absolute, err := filepath.Abs(directory)
			if err != nil {
				return nil, err
			}
			directory = absolute
		}
		result.DatabaseConfig = SourceDatabaseConfig{Kind: "pglite", Options: &PGliteOptions{DataDir: directory}}
	default:
		return nil, fmt.Errorf("unknown database kind %q", kind)
	}
	return result, nil
}
