package main

import (
	"fmt"
	"strings"

	"cloud.google.com/go/spanner/admin/database/apiv1/databasepb"
)

// composeSpannerDSN builds the DSN string for go-sql-spanner. Non-empty suffix is appended
// after the database path, separated by ';'.
// For PostgreSQL dialect, dialect=POSTGRESQL is prepended before suffix when suffix does not
// already set dialect= (emulator auto-create and alignment with parser.Split).
func composeSpannerDSN(project, instance, database string, dialect databasepb.DatabaseDialect, suffix string) string {
	base := fmt.Sprintf("projects/%s/instances/%s/databases/%s", project, instance, database)
	suffix = strings.TrimSpace(suffix)
	suffix = strings.TrimPrefix(suffix, ";")
	suffix = strings.TrimSpace(suffix)

	var segs []string
	if dialect == databasepb.DatabaseDialect_POSTGRESQL && !dsnHasDialectParam(suffix) {
		segs = append(segs, "dialect=POSTGRESQL")
	}
	if suffix != "" {
		segs = append(segs, suffix)
	}
	if len(segs) == 0 {
		return base
	}
	return base + ";" + strings.Join(segs, ";")
}

func dsnHasDialectParam(suffix string) bool {
	for _, part := range strings.Split(suffix, ";") {
		k := strings.TrimSpace(part)
		if i := strings.IndexByte(k, '='); i >= 0 {
			k = strings.TrimSpace(k[:i])
		}
		if strings.EqualFold(k, "dialect") {
			return true
		}
	}
	return false
}
