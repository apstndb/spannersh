package dbsqlrows

import (
	"database/sql"
	"fmt"

	sppb "cloud.google.com/go/spanner/apiv1/spannerpb"
)

// ReadMetadataAndAdvanceToData reads the metadata pseudo-row and advances to the data result set.
// If there is no next row, returns ok=false and err=rows.Err() (nil on clean EOF).
func ReadMetadataAndAdvanceToData(rows *sql.Rows) (*sppb.ResultSetMetadata, bool, error) {
	if rows == nil {
		return nil, false, ErrNilRows
	}
	fac := sqlRowsFacade{rows}
	if !fac.next() {
		return nil, false, fac.err()
	}
	var md *sppb.ResultSetMetadata
	if err := fac.scan(&md); err != nil {
		return nil, false, err
	}
	if !fac.nextResultSet() {
		return nil, false, fmt.Errorf("expected data rows result set after metadata")
	}
	return md, true, nil
}
