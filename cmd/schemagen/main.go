// Command schemagen applies the GORM models to an empty scratch database at DATABASE_URL so Atlas can diff that schema against the migrations directory. Never point it at a real database.
package main

import (
	"errors"
	"fmt"
	"os"

	"go.icco.me/etu-backend/internal/db"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "schemagen: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	database, err := db.New()
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	if err := database.AutoMigrate(); err != nil {
		return errors.Join(fmt.Errorf("automigrate: %w", err), database.Close())
	}
	return database.Close()
}
