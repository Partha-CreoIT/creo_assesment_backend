package config

import (
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	Port           string
	DatabaseURL    string
	JWTSecret      string
	AdminEmail     string
	AdminPassword  string
	PistonURL       string
	RunnerMode      string // auto | piston | docker
	RunnerContainer string
	CORSOrigins     []string
	AnswerGraceSec  int

	// Code-run / grading tunables — see README's "Code runner" section.
	RunConcurrency     int // max concurrent /me/run executions, process-wide
	GraderWorkers      int // background grading worker pool size
	RunQueueTimeoutSec int // how long a run request waits for a concurrency slot before 429
	RunRateLimitSec    int // minimum seconds between /me/run calls for a single session
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// getenvInt parses an int env var, falling back to def if unset, invalid, or negative.
func getenvInt(key string, def int) int {
	n, err := strconv.Atoi(getenv(key, strconv.Itoa(def)))
	if err != nil || n < 0 {
		return def
	}
	return n
}

func Load() *Config {
	_ = godotenv.Load()

	origins := []string{}
	for _, o := range strings.Split(getenv("CORS_ORIGINS", "http://localhost:3000"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			origins = append(origins, o)
		}
	}

	return &Config{
		Port:           getenv("PORT", "8080"),
		DatabaseURL:    getenv("DATABASE_URL", "postgres://exam:exam@localhost:5433/exam_taker?sslmode=disable"),
		JWTSecret:      getenv("JWT_SECRET", "dev-secret-change-me"),
		AdminEmail:     getenv("ADMIN_EMAIL", "admin@example.com"),
		AdminPassword:  getenv("ADMIN_PASSWORD", "admin123"),
		PistonURL:       getenv("PISTON_URL", "http://localhost:2000"),
		RunnerMode:      getenv("RUNNER", "auto"),
		RunnerContainer: getenv("RUNNER_CONTAINER", "exam_taker_runner"),
		CORSOrigins:     origins,
		AnswerGraceSec:  getenvInt("ANSWER_GRACE_SEC", 30),

		RunConcurrency:     getenvInt("RUN_CONCURRENCY", 4),
		GraderWorkers:      getenvInt("GRADER_WORKERS", 4),
		RunQueueTimeoutSec: getenvInt("RUN_QUEUE_TIMEOUT_SEC", 15),
		RunRateLimitSec:    getenvInt("RUN_RATE_LIMIT_SEC", 2),
	}
}
