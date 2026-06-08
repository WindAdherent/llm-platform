package main

import (
	"fmt"
	"log"

	"github.com/WindAdherent/llm-platform/internal/config"
	"github.com/WindAdherent/llm-platform/internal/server"
)

func main() {
	cfg := config.Load()

	r := server.NewRouter(cfg)

	addr := fmt.Sprintf("%s:%s", cfg.AppHost, cfg.AppPort)
	log.Printf("Starting %s on %s, env=%s", cfg.AppName, addr, cfg.AppEnv)

	if err := r.Run(addr); err != nil {
		log.Fatalf("failed to start server: %v", err)
	}
}
