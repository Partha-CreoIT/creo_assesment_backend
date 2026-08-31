package main

import (
	"log"

	"exam_taker_bc/internal/config"
	"exam_taker_bc/internal/database"
	"exam_taker_bc/internal/seed"
)

func main() {
	cfg := config.Load()
	db, err := database.Connect(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	if err := database.Migrate(db); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	if err := database.EnsureAdmin(db, cfg.AdminEmail, cfg.AdminPassword); err != nil {
		log.Fatalf("ensure admin: %v", err)
	}
	if err := seed.Run(db); err != nil {
		log.Fatalf("seed: %v", err)
	}
	log.Println("seed complete")
}
