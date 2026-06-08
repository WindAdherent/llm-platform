package main

import (
	"context"
	"log"

	"github.com/WindAdherent/llm-platform/internal/cache"
	"github.com/WindAdherent/llm-platform/internal/config"
	"github.com/WindAdherent/llm-platform/internal/database"
	"github.com/WindAdherent/llm-platform/internal/server"
	"github.com/WindAdherent/llm-platform/internal/storage"
)

func main() {
	ctx := context.Background()

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

	rdb, err := cache.ConnectRedis(ctx, cfg)
	if err != nil {
		log.Fatalf("failed to connect redis: %v", err)
	}
	defer rdb.Close()

	objectStorage, err := storage.ConnectMinIO(ctx, cfg)
	if err != nil {
		log.Fatalf("failed to connect minio: %v", err)
	}

	if err := objectStorage.EnsureBucket(ctx); err != nil {
		log.Fatalf("failed to ensure minio bucket: %v", err)
	}

	r := server.NewRouter(cfg, db, rdb, objectStorage)

	log.Printf("Starting %s on %s, env=%s", cfg.AppName, cfg.HTTPAddr(), cfg.AppEnv)

	if err := r.Run(cfg.HTTPAddr()); err != nil {
		log.Fatalf("failed to start server: %v", err)
	}
}
