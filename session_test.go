package main

import (
	"database/sql"
	"strings"
	"testing"
)

func TestAcquireSessionConnClosedDB(t *testing.T) {
	db, err := sql.Open("spanner", "projects/p/instances/i/databases/d")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = acquireSessionConn(t.Context(), db)
	if err == nil || !strings.Contains(err.Error(), "session connection") {
		t.Fatalf("err = %v", err)
	}
}

func TestExecuteQueryRequiresSessionConn(t *testing.T) {
	_, err := executeQuery(t.Context(), nil, preparedQuery{execSQL: "SELECT 1"})
	if err == nil || !strings.Contains(err.Error(), "session connection is not open") {
		t.Fatalf("err = %v", err)
	}
}
