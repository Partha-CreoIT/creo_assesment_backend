package main

import (
	"log"
	"time"

	"exam_taker_bc/internal/config"
	"exam_taker_bc/internal/database"
	"exam_taker_bc/internal/handlers"
	"exam_taker_bc/internal/runner"
	"exam_taker_bc/internal/services"
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

	runnerClient := runner.Select(cfg.RunnerMode, cfg.PistonURL, cfg.RunnerContainer)
	grader := services.NewGrader(db, runnerClient)
	grader.Start(cfg.GraderWorkers)
	grader.RequeueStuck()
	services.StartSweeper(db, grader, cfg.AnswerGraceSec)

	runLimiter := handlers.NewRunLimiter(
		cfg.RunConcurrency,
		time.Duration(cfg.RunRateLimitSec)*time.Second,
		time.Duration(cfg.RunQueueTimeoutSec)*time.Second,
	)
	api := &handlers.API{DB: db, Cfg: cfg, Grader: grader, Runner: runnerClient, RunLimiter: runLimiter}
	r := api.BuildRouter()

	log.Printf("exam_taker backend listening on :%s (runner: %s)", cfg.Port, runnerClient.Name())
	if err := r.Run(":" + cfg.Port); err != nil {
		log.Fatal(err)
	}
}
