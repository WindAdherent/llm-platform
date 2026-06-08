package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/WindAdherent/llm-platform/internal/cache"
	"github.com/WindAdherent/llm-platform/internal/config"
	"github.com/WindAdherent/llm-platform/internal/database"
	"github.com/WindAdherent/llm-platform/internal/worker"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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

	taskCache := cache.NewTaskCache(rdb)
	modelDownloadWorker := worker.NewModelDownloadWorker(db, taskCache)

	if err := modelDownloadWorker.Run(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			log.Println("worker exited")
			return
		}

		log.Fatalf("worker failed: %v", err)
	}
}
