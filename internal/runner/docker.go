package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/google/uuid"
)

// DockerRunner executes code inside a long-running, network-isolated container
// (see runner-image/ and the `runner` service in docker-compose.yml) via
// `docker exec`. It is the native-arch fallback for machines where Piston's
// isolate sandbox can't run (e.g. amd64 emulation on Apple Silicon).
//
// Isolation: non-root user, no network, container-level memory/pids caps,
// per-run kill timeout, tmpfs work directory.
type DockerRunner struct {
	Container string
}

func NewDockerRunner(container string) *DockerRunner {
	if container == "" {
		container = "exam_taker_runner"
	}
	return &DockerRunner{Container: container}
}

func (d *DockerRunner) Name() string { return "docker-exec" }

const (
	maxCapturedOutput = 64 * 1024
	compileTimeoutSec = 25
)

type stepResult struct {
	stdout   string
	stderr   string
	exitCode int
	timedOut bool
}

// runStep executes a command inside the runner container with stdin and a hard timeout.
func (d *DockerRunner) runStep(ctx context.Context, workdir string, stdin string, timeoutSec int, command ...string) (*stepResult, error) {
	args := []string{"exec", "-i", "-w", workdir, d.Container,
		"timeout", "-s", "KILL", fmt.Sprint(timeoutSec)}
	args = append(args, command...)

	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec+15)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &stdout}
	cmd.Stderr = &limitedWriter{w: &stderr}

	err := cmd.Run()
	res := &stepResult{stdout: stdout.String(), stderr: stderr.String()}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			res.exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("docker exec: %w", err)
		}
	}
	// Environment failures must surface as errors (so grading retries),
	// never as "the student's program failed".
	if strings.Contains(res.stderr, "Error response from daemon") ||
		strings.Contains(res.stderr, "Cannot connect to the Docker daemon") ||
		res.exitCode == 125 || res.exitCode == 126 {
		return nil, fmt.Errorf("runner container error (exit %d): %.200s", res.exitCode, res.stderr)
	}
	// `timeout -s KILL` exits 137 (128+9) when it kills the process.
	if res.exitCode == 137 || ctx.Err() != nil {
		res.timedOut = true
	}
	return res, nil
}

func (d *DockerRunner) sh(ctx context.Context, script string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "exec", d.Container, "sh", "-c", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker exec sh: %v: %s", err, string(out))
	}
	return nil
}

func (d *DockerRunner) writeFile(ctx context.Context, path, content string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", d.Container, "sh", "-c", "cat > "+path)
	cmd.Stdin = strings.NewReader(content)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("write file: %v: %s", err, string(out))
	}
	return nil
}

func (d *DockerRunner) Execute(ctx context.Context, language, code, stdin string, timeLimitMS, memoryKB int) (*ExecResult, error) {
	fileName, ok := fileNames[language]
	if !ok {
		return nil, fmt.Errorf("unsupported language %q", language)
	}
	if timeLimitMS <= 0 {
		timeLimitMS = 3000
	}
	runTimeoutSec := (timeLimitMS + 999) / 1000
	if runTimeoutSec < 1 {
		runTimeoutSec = 1
	}

	workdir := "/home/runner/work/" + uuid.NewString()
	if err := d.sh(ctx, "mkdir -p "+workdir); err != nil {
		return nil, err
	}
	defer func() {
		// best-effort cleanup, off the request path
		go func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			_ = d.sh(cleanupCtx, "rm -rf "+workdir)
		}()
	}()

	if err := d.writeFile(ctx, workdir+"/"+fileName, code); err != nil {
		return nil, err
	}

	result := &ExecResult{CompileOK: true}

	// compile stage
	var compileCmd []string
	switch language {
	case "c":
		compileCmd = []string{"gcc", "main.c", "-O2", "-lm", "-o", "prog"}
	case "java":
		compileCmd = []string{"javac", "Main.java"}
	}
	if compileCmd != nil {
		cres, err := d.runStep(ctx, workdir, "", compileTimeoutSec, compileCmd...)
		if err != nil {
			return nil, err
		}
		result.CompileOutput = cres.stdout + cres.stderr
		if cres.timedOut || cres.exitCode != 0 {
			result.CompileOK = false
			return result, nil
		}
	}

	// run stage
	var runCmd []string
	switch language {
	case "python":
		runCmd = []string{"python3", "main.py"}
	case "c":
		runCmd = []string{"./prog"}
	case "java":
		runCmd = []string{"java", "-Xmx256m", "-XX:+UseSerialGC", "Main"}
	}
	rres, err := d.runStep(ctx, workdir, stdin, runTimeoutSec, runCmd...)
	if err != nil {
		return nil, err
	}
	result.Stdout = rres.stdout
	result.Stderr = rres.stderr
	result.Output = rres.stdout + rres.stderr
	result.ExitCode = rres.exitCode
	result.TimedOut = rres.timedOut
	if rres.timedOut {
		result.Signal = "SIGKILL"
	}
	return result, nil
}

// Health checks that the runner container is up and has the toolchain.
func (d *DockerRunner) Health(ctx context.Context) error {
	return d.sh(ctx, "python3 --version && gcc --version >/dev/null && javac -version")
}

type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n >= maxCapturedOutput {
		return len(p), nil // swallow the rest
	}
	remain := maxCapturedOutput - l.n
	if len(p) > remain {
		l.w.Write(p[:remain])
		l.n = maxCapturedOutput
		return len(p), nil
	}
	l.n += len(p)
	return l.w.Write(p)
}
