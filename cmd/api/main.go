package main

import (
	"log"

	"github.com/WindAdherent/llm-platform/internal/config"
	"github.com/WindAdherent/llm-platform/internal/database"
	"github.com/WindAdherent/llm-platform/internal/server"
)

func main() {
	cfg := config.Load()

	db, err := database.ConnectMySQL(cfg)
	if err != nil {
		log.Fatalf("failed to connect mysql: %v", err)
	}

	if err := database.AutoMigrate(db); err != nil {
		log.Fatalf("failed to run database migration: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("failed to get raw database connection: %v", err)
	}
	defer sqlDB.Close()

	r := server.NewRouter(cfg, db)

	log.Printf("Starting %s on %s, env=%s", cfg.AppName, cfg.HTTPAddr(), cfg.AppEnv)

	if err := r.Run(cfg.HTTPAddr()); err != nil {
		log.Fatalf("failed to start server: %v", err)
	}
}
