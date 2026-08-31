package runner

import (
	"context"
	"log"
	"strings"
	"time"
)

// Select picks the code runner.
//
//	mode "piston" — always use the Piston HTTP engine
//	mode "docker" — always use the docker-exec runner container
//	mode "auto"   — probe Piston with a real execution; fall back to docker-exec
//	                (Piston's isolate sandbox cannot run under amd64 emulation
//	                on Apple Silicon, so auto-detection keeps dev machines working)
func Select(mode, pistonURL, dockerContainer string) Runner {
	piston := New(pistonURL)
	dockerRunner := NewDockerRunner(dockerContainer)

	switch mode {
	case "piston":
		return piston
	case "docker":
		return dockerRunner
	}

	// auto: give piston up to ~30s to come up (it usually boots with docker compose)
	const attempts = 8
	for i := 1; i <= attempts; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		// 3000ms run limit keeps the probe valid even on a default-configured piston
		res, err := piston.Execute(ctx, "python", "print(6*7)", "", 3000, 0)
		cancel()
		if err == nil && strings.TrimSpace(res.Stdout) == "42" {
			log.Printf("runner: piston OK at %s", pistonURL)
			return piston
		}
		if err == nil {
			// piston answered but can't actually execute (e.g. isolate broken
			// under amd64 emulation) — no point retrying
			log.Printf("runner: piston responds but cannot execute (stdout=%q stderr=%.120q)",
				res.Stdout, res.Stderr)
			break
		}
		log.Printf("runner: piston probe %d/%d failed: %v", i, attempts, err)
		time.Sleep(3 * time.Second)
	}

	hctx, hcancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer hcancel()
	if err := dockerRunner.Health(hctx); err == nil {
		log.Printf("runner: using docker-exec container %q", dockerRunner.Container)
		return dockerRunner
	}
	log.Printf("runner: docker-exec container %q not available either — staying on piston; "+
		"code runs will fail until piston is up (or start the fallback with "+
		"`docker compose --profile fallback up -d runner` and set RUNNER=docker)",
		dockerRunner.Container)
	return piston
}
