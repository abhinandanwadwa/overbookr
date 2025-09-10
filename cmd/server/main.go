package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/abhinandanwadwa/overbookr/internal/api/server"
	workers "github.com/abhinandanwadwa/overbookr/internal/worker"
	"github.com/jackc/pgx/v5"
	"github.com/joho/godotenv"
)

func main() {
	// Load context and envs
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}
	DB_URI := os.Getenv("POSTGRESQL_URI")
	PORT := os.Getenv("PORT")
	cfg := server.Config{
		DB_URI: DB_URI,
		PORT:   PORT,
	}

	// Connect to DB
	conn, err := pgx.Connect(context.Background(), cfg.DB_URI)
	if err != nil {
		log.Fatal("Unable to connect to database:", err)
		os.Exit(1)
	}
	defer conn.Close(ctx)

	// --- Workers setup ---
	// Create worker instances bound to the same DB connection
	holdExpiryWorker := workers.NewHoldExpiryWorker(conn)
	reconcileWorker := workers.NewReconcileWorker(conn)

	// 1) Start hold expiry loop (every 30s)
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := holdExpiryWorker.ExpireHolds(context.Background()); err != nil {
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
	srv := server.NewServer(cfg, conn)
	if err := srv.Start(); err != nil {
		log.Printf("server exited: %v", err)
		os.Exit(1)
	}
}
