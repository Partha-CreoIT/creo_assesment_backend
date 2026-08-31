package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Client talks to a Piston code-execution engine.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func New(baseURL string) *Client {
	return &Client{
		BaseURL: baseURL,
		HTTP:    &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *Client) Name() string { return "piston" }

type ExecResult struct {
	CompileOutput string `json:"compileOutput"`
	CompileOK     bool   `json:"compileOk"`
	Stdout        string `json:"stdout"`
	Stderr        string `json:"stderr"`
	Output        string `json:"output"`
	ExitCode      int    `json:"exitCode"`
	Signal        string `json:"signal"`
	TimedOut      bool   `json:"timedOut"`
}

var fileNames = map[string]string{
	"python": "main.py",
	"java":   "Main.java", // students must keep the class named Main
	"c":      "main.c",
}

type pistonStage struct {
	Stdout string  `json:"stdout"`
	Stderr string  `json:"stderr"`
	Output string  `json:"output"`
	Code   *int    `json:"code"`
	Signal *string `json:"signal"`
}

type pistonResponse struct {
	Message string       `json:"message"`
	Run     *pistonStage `json:"run"`
	Compile *pistonStage `json:"compile"`
}

// Execute compiles and runs code with the given stdin.
// language must be one of python / java / c. timeLimitMS bounds the run stage.
func (c *Client) Execute(ctx context.Context, language, code, stdin string, timeLimitMS, memoryKB int) (*ExecResult, error) {
	fileName, ok := fileNames[language]
	if !ok {
		return nil, fmt.Errorf("unsupported language %q", language)
	}
	if timeLimitMS <= 0 {
		timeLimitMS = 3000
	}
	// stay within piston's configured run_timeout cap (raised to 10s in compose)
	if timeLimitMS > 10000 {
		timeLimitMS = 10000
	}
	memLimit := int64(-1)
	if memoryKB > 0 {
		memLimit = int64(memoryKB) * 1024
	}

	payload := map[string]any{
		"language": language,
		"version":  "*",
		"files":    []map[string]string{{"name": fileName, "content": code}},
		"stdin":    stdin,
		// piston's default hard cap for compile_timeout is 10000ms
		"compile_timeout":      10000,
		"run_timeout":          timeLimitMS,
		"compile_memory_limit": int64(-1),
		"run_memory_limit":     memLimit,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/v2/execute", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("piston request: %w", err)
	}
	defer resp.Body.Close()

	var pr pistonResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, fmt.Errorf("piston decode: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := pr.Message
		if msg == "" {
			msg = fmt.Sprintf("piston returned HTTP %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("%s", msg)
	}

	res := &ExecResult{CompileOK: true}
	if pr.Compile != nil {
		res.CompileOutput = pr.Compile.Output
		if pr.Compile.Code != nil && *pr.Compile.Code != 0 {
			res.CompileOK = false
		}
	}
	if pr.Run != nil {
		res.Stdout = pr.Run.Stdout
		res.Stderr = pr.Run.Stderr
		res.Output = pr.Run.Output
		if pr.Run.Code != nil {
			res.ExitCode = *pr.Run.Code
		}
		if pr.Run.Signal != nil {
			res.Signal = *pr.Run.Signal
			if *pr.Run.Signal == "SIGKILL" {
				res.TimedOut = true
			}
		}
	}
	return res, nil
}

// Health pings the runtimes endpoint.
func (c *Client) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/v2/runtimes", nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("piston health: HTTP %d", resp.StatusCode)
	}
	return nil
}
