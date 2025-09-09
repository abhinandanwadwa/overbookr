package main

import (
	"context"
	"log"
	"os"

	"github.com/abhinandanwadwa/overbookr/internal/api/server"
	"github.com/jackc/pgx/v5"
	"github.com/joho/godotenv"
)

func main() {
	// Load context and envs
	ctx := context.Background()
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

	srv := server.NewServer(cfg, conn)
	if err := srv.Start(); err != nil {
		log.Printf("server exited: %v", err)
		os.Exit(1)
	}

	// dbx := db.New(conn)
	// events, err := dbx.GetAllEvents(ctx)
	// if err != nil {
	// 	log.Fatal("Error fetching events:", err)
	// }
	// fmt.Println("Events:", events)
}
