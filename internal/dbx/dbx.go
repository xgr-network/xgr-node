package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
)

var (
	sqlOnce sync.Once
	sqlDB   *sql.DB
	sqlErr  error

	poolOnce sync.Once
	poolDB   *pgxpool.Pool
	poolErr  error
)

func envDSN() (string, error) {
	dsn := strings.TrimSpace(os.Getenv("XGR_DB_DSN"))
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv("DATABASE_URL"))
	}
	if dsn == "" {
		return "", fmt.Errorf("database not configured (XGR_DB_DSN / DATABASE_URL)")
	}
	return dsn, nil
}

func envSQLDriver() string {
	driver := strings.TrimSpace(os.Getenv("XGR_DB_DRIVER"))
	if driver == "" {
		driver = "pgx"
	}
	return driver
}

// GetSQL returns the process-wide singleton *sql.DB.
func GetSQL(ctx context.Context) (*sql.DB, error) {
	sqlOnce.Do(func() {
		dsn, err := envDSN()
		if err != nil {
			sqlErr = err
			return
		}
		driver := envSQLDriver()

		db, err := sql.Open(driver, dsn)
		if err != nil {
			sqlErr = err
			return
		}

		// Stable defaults (tune later).
		db.SetMaxOpenConns(30)
		db.SetMaxIdleConns(15)
		db.SetConnMaxLifetime(30 * time.Minute)
		db.SetConnMaxIdleTime(2 * time.Minute)

		pingCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := db.PingContext(pingCtx); err != nil {
			_ = db.Close()
			sqlErr = err
			return
		}
		sqlDB = db
	})

	// Optional: quick health ping on reuse to catch stale TCP.
	if sqlDB != nil {
		pingCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
		defer cancel()
		_ = sqlDB.PingContext(pingCtx)
	}

	return sqlDB, sqlErr
}

// NewPGXPool creates a new pgxpool.Pool with the same standard settings.
// Use only when you explicitly want a dedicated pool (e.g. tests).
func NewPGXPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	if strings.TrimSpace(dsn) == "" {
		var err error
		dsn, err = envDSN()
		if err != nil {
			return nil, err
		}
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}

	// Stable defaults (tune later).
	cfg.MaxConns = 30
	cfg.MinConns = 2
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 2 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}

	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// GetPGXPool returns the process-wide singleton *pgxpool.Pool.
func GetPGXPool(ctx context.Context) (*pgxpool.Pool, error) {
	poolOnce.Do(func() {
		pool, err := NewPGXPool(ctx, "")
		if err != nil {
			poolErr = err
			return
		}
		poolDB = pool
	})
	return poolDB, poolErr
}
