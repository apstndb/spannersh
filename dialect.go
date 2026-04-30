package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/spanner/admin/database/apiv1/databasepb"
)

// Client-side SHOW forms for the read-only connection property database_dialect (go-sql-spanner v1.25.0+).
// Parser dialect matches the connected database: GoogleSQL requires VARIABLE; PostgreSQL omits it.
const (
	sqlShowDatabaseDialectGoogleSQL  = `SHOW VARIABLE database_dialect`
	sqlShowDatabaseDialectPostgreSQL = `SHOW database_dialect`
)

// parseCLIDialect maps --dialect to a dialect and/or auto-detect mode for client-side parser.Split
// (same family as go-sql-spanner's StatementParser). When autoDetect is true, the returned dialect
// value is ignored until [detectDatabaseDialect] runs after connect.
func parseCLIDialect(s string) (d databasepb.DatabaseDialect, autoDetect bool, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return databasepb.DatabaseDialect_GOOGLE_STANDARD_SQL, false, nil
	}
	norm := strings.ToUpper(strings.ReplaceAll(s, "-", "_"))
	switch norm {
	case "AUTO":
		return databasepb.DatabaseDialect_DATABASE_DIALECT_UNSPECIFIED, true, nil
	case "GOOGLE_STANDARD_SQL", "GOOGLESQL", "GOOGLE_SQL":
		return databasepb.DatabaseDialect_GOOGLE_STANDARD_SQL, false, nil
	case "POSTGRESQL", "POSTGRES", "PG":
		return databasepb.DatabaseDialect_POSTGRESQL, false, nil
	}
	if v, ok := databasepb.DatabaseDialect_value[norm]; ok {
		dd := databasepb.DatabaseDialect(v)
		if dd != databasepb.DatabaseDialect_DATABASE_DIALECT_UNSPECIFIED {
			return dd, false, nil
		}
	}
	return 0, false, fmt.Errorf("invalid dialect %q (use auto, google-standard-sql, or postgresql)", s)
}

// dialectFromDatabaseOptionValue maps a dialect name string (e.g. GOOGLE_STANDARD_SQL) to [databasepb.DatabaseDialect].
func dialectFromDatabaseOptionValue(name string) (databasepb.DatabaseDialect, error) {
	if v, ok := databasepb.DatabaseDialect_value[name]; ok {
		return databasepb.DatabaseDialect(v), nil
	}
	return 0, fmt.Errorf("unknown database dialect: %s", name)
}

// queryDriverDatabaseDialect reads the driver’s read-only [database_dialect] connection property via
// client-side SHOW (no extra INFORMATION_SCHEMA round trip—the connector already populated this at
// connect; see go-sql-spanner [determineDialect] and [propertyDatabaseDialect]).
func queryDriverDatabaseDialect(ctx context.Context, db *sql.DB) (databasepb.DatabaseDialect, error) {
	name, err := queryDriverDatabaseDialectValue(ctx, db, sqlShowDatabaseDialectGoogleSQL)
	if err == nil {
		return dialectFromDatabaseOptionValue(name)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return 0, fmt.Errorf("show database_dialect with GoogleSQL syntax: %w", err)
	}
	name, err2 := queryDriverDatabaseDialectValue(ctx, db, sqlShowDatabaseDialectPostgreSQL)
	if err2 != nil {
		return 0, fmt.Errorf("show database_dialect: %w", errors.Join(
			fmt.Errorf("googlesql syntax: %w", err),
			fmt.Errorf("postgresql syntax: %w", err2),
		))
	}
	return dialectFromDatabaseOptionValue(name)
}

func queryDriverDatabaseDialectValue(ctx context.Context, db *sql.DB, sqlText string) (string, error) {
	var name string
	err := db.QueryRowContext(ctx, sqlText).Scan(&name)
	if err != nil {
		return "", err
	}
	return name, nil
}

// detectDatabaseDialect sets spannersh’s client-side StatementParser dialect from the driver’s
// database_dialect property. With --dialect auto, this also serves as synchronous connection warm-up
// (see warmup.go). Use an explicit --dialect if this fails.
func detectDatabaseDialect(ctx context.Context, db *sql.DB) (databasepb.DatabaseDialect, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	d, err := queryDriverDatabaseDialect(ctx, db)
	if err != nil {
		return 0, err
	}
	if d == databasepb.DatabaseDialect_DATABASE_DIALECT_UNSPECIFIED {
		return 0, errors.New("database_dialect is unspecified")
	}
	return d, nil
}

func effectiveStatementDialect(d databasepb.DatabaseDialect) databasepb.DatabaseDialect {
	if d == databasepb.DatabaseDialect_DATABASE_DIALECT_UNSPECIFIED {
		return databasepb.DatabaseDialect_GOOGLE_STANDARD_SQL
	}
	return d
}
