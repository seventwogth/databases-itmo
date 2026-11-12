// Package db хранит SQL-миграции схемы и функций из docs/sql-functions.md.
package db

import "embed"

// Migrations — файлы migrations/*.sql, применяются по возрастанию имени.
//
//go:embed migrations/*.sql
var Migrations embed.FS
