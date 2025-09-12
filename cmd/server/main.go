package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/abhinandanwadwa/overbookr/internal/api/server"
	"github.com/abhinandanwadwa/overbookr/internal/workers"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

func main() {
	// Load context and envs
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := godotenv.Load()
	if err != nil {
		log.Println("No .env file found — continuing with environment variables")
	}
	DB_URI := os.Getenv("POSTGRESQL_URI")
	PORT := os.Getenv("PORT")
	if DB_URI == "" {
		log.Fatal("POSTGRESQL_URI is required")
	}

	cfg := server.Config{
		DB_URI: DB_URI,
		PORT:   PORT,
	}

	// Create a connection pool for workers
	pool, err := pgxpool.New(ctx, cfg.DB_URI)
	if err != nil {
		log.Fatalf("unable to create pgx pool: %v", err)
	}
	defer pool.Close()

	// --- Workers setup ---
	// Create worker instances bound to the same DB connection
	holdExpiryWorker := workers.NewHoldExpiryWorker(pool)
	reconcileWorker := workers.NewReconcileWorker(pool)

	// 1) Start hold expiry loop (every 30s)
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Println("hold expiry loop stopping")
				return
			case <-ticker.C:
				if err := holdExpiryWorker.ExpireHolds(ctx); err != nil {
					log.Printf("hold expiry worker error: %v\n", err)
				}
			}
		}
	}()

	// 2) Start reconcile loop (every 1 hour)
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := reconcileWorker.Reconcile(context.Background()); err != nil {
					log.Printf("reconcile worker error: %v\n", err)
				}
			}
		}
	}()

	// --- Server start ---
	srv := server.NewServer(cfg, pool)
	if err := srv.Start(); err != nil {
		log.Printf("server exited: %v", err)
		os.Exit(1)
	}
}
