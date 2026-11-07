package main

import (
	"go/parser"
	"go/token"
	"reflect"
	"testing"
)

func TestExtractProperties(t *testing.T) {
	// Include the PostgreSQL wrappers introduced by go-sql-spanner v1.27.0.
	const src = `package driver
var common = createConnectionProperty("endpoint", "Target " + "endpoint.", "", false, nil, connectionstate.ContextStartup)
var readOnly = createReadOnlyConnectionProperty("database_dialect", "Database dialect.", "", false, nil, connectionstate.ContextUser)
func init() {
    addPostgresProp("application_name", "Application name.", "", false, converter)
    addReadOnlyPostgresProp("server_version", "Server version.", "14.1")
    addPostgresProp(name, description, defaultValue, true, converter)
}
`
	f, err := parser.ParseFile(token.NewFileSet(), "connection_properties.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []prop{
		{Name: "endpoint", Description: "Target endpoint.", Context: "ContextStartup"},
		{Name: "database_dialect", Description: "Database dialect.", Context: "ContextUser", ReadOnly: true},
		{Name: "application_name", Description: "Application name.", Context: "ContextUser", PostgreSQLOnly: true},
		{Name: "server_version", Description: "Server version.", Context: "ContextUser", ReadOnly: true, PostgreSQLOnly: true},
	}
	if got := extractProperties(f); !reflect.DeepEqual(got, want) {
		t.Fatalf("extractProperties() = %#v, want %#v", got, want)
	}
}
