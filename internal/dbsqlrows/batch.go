package dbsqlrows

import (
	"database/sql"
	"fmt"

	sppb "cloud.google.com/go/spanner/apiv1/spannerpb"
)

// StatementSpec is one metadata → data → optional stats cycle in a multi-statement batch.
type StatementSpec struct {
	Hooks   SQLRowsHooks
	Options RunOptions
}

// RunBatch runs consecutive statement cycles on rows. initialMD is metadata for the first
// statement; rows must already be positioned on its data result set.
func RunBatch(rows *sql.Rows, initialMD *sppb.ResultSetMetadata, specs []StatementSpec) ([]SQLRowsResult, error) {
	if rows == nil {
		return nil, ErrNilRows
	}
	if initialMD == nil {
		return nil, ErrNilMetadata
	}
	if len(specs) == 0 {
		return nil, nil
	}

	results := make([]SQLRowsResult, 0, len(specs))
	md := initialMD
	for i, spec := range specs {
		if i > 0 {
			nextMD, ok, err := ReadMetadataAndAdvanceToData(rows)
			if err != nil {
				return results, err
			}
			if !ok {
				return results, fmt.Errorf("expected metadata for statement %d", i)
			}
			md = nextMD
		}
		opts := spec.Options
		opts.ReadMetadataPseudoRow = false
		if opts.DataMode == 0 {
			opts.DataMode = DataModeProcess
		}
		res, err := Run(rows, md, spec.Hooks, opts)
		if err != nil {
			return results, err
		}
		results = append(results, *res)
	}
	return results, nil
}
