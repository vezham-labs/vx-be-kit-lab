package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDatabaseConfigurationUsesServerEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := ReadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.DatabaseDriver != "pgx" || config.DatabaseURLEnv != "DATABASE_URL" || config.DatabaseURL != "" {
		t.Fatalf("unexpected database defaults: %+v", config)
	}
	t.Setenv("DATABASE_URL", "")
	if _, err = config.DatabaseDSN(); err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatal("missing Neon credentials must not silently select SQLite")
	}
	expected := "postgresql://test:fake-password@ep-example-pooler.us-east-2.aws.neon.tech/neondb?sslmode=require&channel_binding=require"
	t.Setenv("DATABASE_URL", expected)
	if value, err := config.DatabaseDSN(); err != nil || value != expected {
		t.Fatal("connection URL options were not preserved")
	}
	if err = os.WriteFile(path, []byte(`{"databaseDriver":"sqlite"}`), 0600); err != nil {
		t.Fatal(err)
	}
	config, err = ReadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if value, err := config.DatabaseDSN(); err != nil || value != ".vezham/tamizhi/data/tamizhi.sqlite" {
		t.Fatal("explicit SQLite development configuration changed")
	}
}
