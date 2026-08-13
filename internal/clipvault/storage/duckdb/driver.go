// Package duckdb owns the DuckDB persistence boundary, including driver
// registration. Importing this package is sufficient for storage consumers to
// open the "duckdb" database/sql driver.
package duckdb

import _ "github.com/duckdb/duckdb-go/v2"
