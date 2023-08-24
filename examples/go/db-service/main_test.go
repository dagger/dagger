package main

import (
	"database/sql"
	"fmt"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"os"
	"testing"
)

func TestDatabase(t *testing.T) {
	db_host := os.Getenv("DB_HOST")
	db_name := os.Getenv("DB_NAME")
	db_password := os.Getenv("DB_PASSWORD")
	db_user := os.Getenv("DB_USER")
	connStr := fmt.Sprintf("user=%s password=%s host=%s dbname=%s sslmode=disable", db_user, db_password, db_host, db_name)
	db, err := sql.Open("postgres", connStr)

	assert.NoError(t, err)

	var result string
	err = db.QueryRow("SELECT 1").Scan(&result)

	assert.NoError(t, err)
	assert.Equal(t, "1", result)
}
