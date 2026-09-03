.PHONY: run seed test build compose-up compose-down runtimes \
	loadtest-server-start loadtest-server-stop \
	loadtest-monitor-start loadtest-monitor-stop \
	loadtest-verify loadtest-smoke loadtest-remote loadtest-certify

run:
	go run ./cmd/server

seed:
	go run ./cmd/seed

test:
	go test ./...

build:
	go build -o bin/server ./cmd/server && go build -o bin/seed ./cmd/seed

compose-up:
	docker compose up -d

compose-down:
	docker compose down

runtimes:
	bash scripts/install-runtimes.sh

# --- Load testing (see loadtest/README.md) ---------------------------------

# Starts the API natively (not containerized) with its PID recorded so
# loadtest/chaos/restart_api.sh and monitor/poll.sh can target it. Builds and
# runs the compiled binary directly (not `go run`, which forks a separate
# child process — `$!` would capture the wrapper, not the actual server).
loadtest-server-start:
	@mkdir -p loadtest/results
	go build -o bin/loadtest-server ./cmd/server
	@nohup ./bin/loadtest-server > loadtest/results/server.log 2>&1 & echo $$! > loadtest/results/.server.pid
	@sleep 2
	@echo "server started, pid $$(cat loadtest/results/.server.pid)"

loadtest-server-stop:
	@if [ -f loadtest/results/.server.pid ]; then \
		kill $$(cat loadtest/results/.server.pid) 2>/dev/null || true; \
		rm -f loadtest/results/.server.pid; echo "server stopped"; \
	else echo "no pidfile — server not started via loadtest-server-start"; fi

loadtest-monitor-start:
	@mkdir -p loadtest/results
	@nohup bash loadtest/monitor/poll.sh loadtest/results/monitor.csv $$(cat loadtest/results/.server.pid 2>/dev/null) \
		> loadtest/results/monitor.log 2>&1 & echo $$! > loadtest/results/.monitor.pid
	@echo "monitor started, pid $$(cat loadtest/results/.monitor.pid), writing loadtest/results/monitor.csv"

loadtest-monitor-stop:
	@if [ -f loadtest/results/.monitor.pid ]; then \
		kill $$(cat loadtest/results/.monitor.pid) 2>/dev/null || true; \
		rm -f loadtest/results/.monitor.pid; echo "monitor stopped"; \
	else echo "no pidfile — monitor not started via loadtest-monitor-start"; fi

# EXAM_ID=<id> CHECK=all make loadtest-verify
loadtest-verify:
	go run ./loadtest/verify --exam-id=$(EXAM_ID) --check=$(or $(CHECK),all) $(if $(EXPECTED),--expected-count=$(EXPECTED),)

# Small end-to-end proof the harness works before trusting any real numbers
# from it: registers 10 candidates as a synchronized burst, then verifies.
loadtest-smoke:
	@mkdir -p loadtest/results/smoke
	RESULTS_DIR=loadtest/results/smoke VUS=10 k6 run loadtest/k6/scenarios/01_registration.js 2>&1 | tee loadtest/results/smoke/k6.log
	@EXAM_ID=$$(grep -o 'EXAM_ID=[0-9]*' loadtest/results/smoke/k6.log | tail -1 | cut -d= -f2); \
	echo "verifying exam $$EXAM_ID"; \
	go run ./loadtest/verify --exam-id=$$EXAM_ID --check=all --expected-count=10

# Run any one scenario against a deployed server with a specific candidate
# count — the two knobs are BASE_URL and LOAD. Example:
#   make loadtest-remote BASE_URL=https://exam.example.com LOAD=300 SCENARIO=07_run_code.js
# Extra scenario-specific env (e.g. LANGUAGE=java, DURATION=2m) can be passed
# via ENV="LANGUAGE=java DURATION=2m".
loadtest-remote:
	@test -n "$(BASE_URL)" || (echo "BASE_URL is required, e.g. make loadtest-remote BASE_URL=https://... LOAD=300 SCENARIO=01_registration.js" && exit 1)
	@test -n "$(SCENARIO)" || (echo "SCENARIO is required, e.g. SCENARIO=01_registration.js (see loadtest/k6/scenarios/)" && exit 1)
	@mkdir -p loadtest/results/latest
	env BASE_URL=$(BASE_URL) VUS=$(or $(LOAD),10) $(ENV) k6 run loadtest/k6/scenarios/$(SCENARIO)

# The Phase 12 certification run: the full realistic exam simulation with
# admin monitoring + CSV export running concurrently, wrapped with a
# reproducibility manifest. Example:
#   make loadtest-certify BASE_URL=https://exam.example.com LOAD=300 RUN_ID=cert300-20260906-01
loadtest-certify:
	@test -n "$(BASE_URL)" || (echo "BASE_URL is required" && exit 1)
	@test -n "$(LOAD)" || (echo "LOAD is required, e.g. LOAD=300" && exit 1)
	bash loadtest/certify.sh "$(BASE_URL)" "$(LOAD)" "$(or $(RUN_ID),cert$(LOAD)-$(shell date -u +%Y%m%d-%H%M%S))" $(ENV)
