package dbsqlrows

import "database/sql"

// rowsFacade hides *sql.Rows for tests (same idea as spanvalue/dbsqlrows and writer tests).
type rowsFacade interface {
	next() bool
	nextResultSet() bool
	scan(dest ...any) error
	err() error
	columnCount() (int, error)
}

type sqlRowsFacade struct {
	*sql.Rows
}

func (f sqlRowsFacade) next() bool {
	return f.Rows.Next()
}

func (f sqlRowsFacade) nextResultSet() bool {
	return f.Rows.NextResultSet()
}

func (f sqlRowsFacade) scan(dest ...any) error {
	return f.Rows.Scan(dest...)
}

func (f sqlRowsFacade) err() error {
	return f.Rows.Err()
}

func (f sqlRowsFacade) columnCount() (int, error) {
	cols, err := f.Rows.Columns()
	if err != nil {
		return 0, err
	}
	return len(cols), nil
}
