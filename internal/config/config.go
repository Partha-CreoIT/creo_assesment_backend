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
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func Load() *Config {
	_ = godotenv.Load()

	grace, err := strconv.Atoi(getenv("ANSWER_GRACE_SEC", "30"))
	if err != nil || grace < 0 {
		grace = 30
	}

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
		AnswerGraceSec:  grace,
	}
}
