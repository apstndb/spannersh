package dbsqlrows

import sppb "cloud.google.com/go/spanner/apiv1/spannerpb"

// SQLRowsResult holds metadata and optional stats after a run, analogous to
// [github.com/apstndb/spanvalue/writer.RowIteratorResult] for native iterators.
type SQLRowsResult struct {
	Metadata *sppb.ResultSetMetadata
	Stats    *sppb.ResultSetStats
	RowsRead int
}
