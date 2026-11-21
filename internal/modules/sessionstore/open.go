package sessionstore

// Database opening: the product owns where the file lives, the driver and its
// PRAGMAs; the SDK store only applies schema and queries.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (registers the "sqlite" driver)

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/session/sqlstore"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// ModuleID is the stable module identity used by assembly documents.
	ModuleID = "zeokeel/sessionstore"

	// DBFileName is the database file inside the product data directory.
	DBFileName = "zeokeel.db"

	// DefaultBusyTimeout is used when Config.BusyTimeoutMs is zero.
	DefaultBusyTimeout = 5 * time.Second
)

// Config is the module's typed assembly config.
type Config struct {
	// Path overrides the database location. An empty value uses
	// <dataDir>/zeokeel.db; a relative value is resolved against the data
	// directory.
	Path string `json:"path,omitempty"`
	// BusyTimeoutMs is SQLite's busy timeout for lock contention (0 = 5000ms).
	BusyTimeoutMs int `json:"busyTimeoutMs,omitempty"`
}

// storeOptions is the resolved construction input for one database.
type storeOptions struct {
	dataDir     string
	path        string
	busyTimeout time.Duration
	logger      contracts.Logger
}

// openStore opens (and, when needed, creates) the SQLite database and applies
// the SDK schema.
func openStore(opts storeOptions) (*store, error) {
	path, err := resolveDatabasePath(opts.dataDir, opts.path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("sessionstore: create data directory: %w", err)
	}
	busyTimeout := opts.busyTimeout
	if busyTimeout <= 0 {
		busyTimeout = DefaultBusyTimeout
	}
	db, err := sql.Open("sqlite", sqliteDSN(path, busyTimeout))
	if err != nil {
		return nil, fmt.Errorf("sessionstore: open %q: %w", path, err)
	}
	// SQLite serializes writers anyway; one connection per store keeps the
	// per-connection PRAGMAs stable and makes transactions easy to reason about.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sessionstore: ping %q: %w", path, err)
	}
	var logger rig.Logger
	if opts.logger != nil {
		logger = opts.logger
	}
	inner, err := sqlstore.New(ctx, db, logger)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	current := &store{inner: inner, dbPath: path, logger: opts.logger, db: db}
	if current.logger != nil {
		current.logger.Printf("%s: opened database %s", ModuleID, path)
	}
	return current, nil
}

// resolveDatabasePath applies the default/relative-path rules.
func resolveDatabasePath(dataDir, override string) (string, error) {
	dataDir = strings.TrimSpace(dataDir)
	override = strings.TrimSpace(override)
	switch {
	case override == "":
		if dataDir == "" {
			return "", errors.New("sessionstore: data directory is required")
		}
		return filepath.Join(dataDir, DBFileName), nil
	case filepath.IsAbs(override):
		return filepath.Clean(override), nil
	case dataDir != "":
		return filepath.Join(dataDir, override), nil
	default:
		return filepath.Clean(override), nil
	}
}

// sqliteDSN builds the driver DSN with the PRAGMAs applied per connection.
func sqliteDSN(path string, busyTimeout time.Duration) string {
	query := url.Values{}
	query.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeout.Milliseconds()))
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "foreign_keys(1)")
	return path + "?" + query.Encode()
}

// closeDatabase closes the underlying database.
func (s *store) closeDatabase() error {
	if s.db == nil {
		return nil
	}
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("sessionstore: close database: %w", err)
	}
	if s.logger != nil {
		s.logger.Printf("%s: closed database %s", ModuleID, s.dbPath)
	}
	return nil
}
