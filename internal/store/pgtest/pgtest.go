// Package pgtest starts a PostgreSQL with pgvector and pg_bigm for tests.
package pgtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Image is built by `make pg-image` from testdata/pg/Dockerfile.
const Image = "ics-pg:18.6"

var (
	once    sync.Once
	baseURL string
	errBoot error
)

func boot() {
	ctx := context.Background()
	pw := randomHex()
	c, err := postgres.Run(ctx, Image,
		postgres.WithDatabase("postgres"), postgres.WithUsername("postgres"), postgres.WithPassword(pw),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(2*time.Minute)))
	if err != nil {
		errBoot = fmt.Errorf("start %s (run `make pg-image` first): %w", Image, err)
		return
	}
	// The container is shared by every test in the binary; Ryuk removes it when the process exits.
	baseURL, errBoot = c.ConnectionString(ctx, "sslmode=disable")
}

// Start returns a pool on a fresh, empty database so tests do not see each other's rows.
func Start(t *testing.T) *pgxpool.Pool {
	t.Helper()
	once.Do(boot)
	if errBoot != nil {
		t.Fatal(errBoot)
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, baseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := "t_" + randomHex()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	db, err := pgxpool.New(ctx, strings.Replace(baseURL, "/postgres?", "/"+name+"?", 1))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return db
}

func randomHex() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
