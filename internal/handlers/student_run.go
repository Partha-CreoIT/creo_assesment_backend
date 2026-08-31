package handlers

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"exam_taker_bc/internal/middleware"
	"exam_taker_bc/internal/models"
	"exam_taker_bc/internal/services"
)

const (
	maxConcurrentRuns = 4
	minRunInterval    = 2 * time.Second
	maxSampleRuns     = 10
)

var (
	runSem      = make(chan struct{}, maxConcurrentRuns)
	lastRunMu   sync.Mutex
	lastRunAt   = map[uint]time.Time{}
)

func acquireRunSlot(sessionID uint) (release func(), retryAfter time.Duration, ok bool) {
	lastRunMu.Lock()
	if t, exists := lastRunAt[sessionID]; exists {
		if wait := minRunInterval - time.Since(t); wait > 0 {
			lastRunMu.Unlock()
			return nil, wait, false
		}
	}
	lastRunAt[sessionID] = time.Now()
	lastRunMu.Unlock()

	select {
	case runSem <- struct{}{}:
		return func() { <-runSem }, 0, true
	case <-time.After(15 * time.Second):
		return nil, 2 * time.Second, false
	}
}

type runPayload struct {
	QuestionID uint   `json:"questionId"`
	Language   string `json:"language"`
	Code       string `json:"code"`
	Mode       string `json:"mode"` // samples | custom
	Stdin      string `json:"stdin"`
}

type runTestResult struct {
	Index    int    `json:"index"`
	Input    string `json:"input"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Status   string `json:"status"`
	Passed   bool   `json:"passed"`
}

// RunCode executes student code against the visible sample tests or custom stdin.
// Hidden test cases are never run here — only at final grading.
func (a *API) RunCode(c *gin.Context) {
	s := middleware.GetSession(c)
	if !a.sessionWritable(c, s) {
		return
	}
	var p runPayload
	if err := c.ShouldBindJSON(&p); err != nil {
		fail(c, http.StatusBadRequest, "invalid request body")
		return
	}
	if !models.CodingLanguages[p.Language] {
		fail(c, http.StatusBadRequest, "language must be python, java or c")
		return
	}
	if strings.TrimSpace(p.Code) == "" {
		fail(c, http.StatusBadRequest, "write some code first")
		return
	}
	if len(p.Code) > 65536 {
		fail(c, http.StatusBadRequest, "code is too long")
		return
	}
	if p.Mode != "custom" {
		p.Mode = "samples"
	}
	_, q, ok := a.setQuestion(c, s, p.QuestionID)
	if !ok {
		return
	}
	if q.Type != models.QuestionCoding {
		fail(c, http.StatusBadRequest, "not a coding question")
		return
	}

	release, retryAfter, ok := acquireRunSlot(s.ID)
	if !ok {
		c.Header("Retry-After", "2")
		fail(c, http.StatusTooManyRequests, "please wait "+retryAfter.Round(time.Second).String()+" between runs")
		return
	}
	defer release()

	ctx := c.Request.Context()

	if p.Mode == "custom" {
		if len(p.Stdin) > 10000 {
			fail(c, http.StatusBadRequest, "custom input is too long")
			return
		}
		execCtx, cancel := context.WithTimeout(ctx, 55*time.Second)
		defer cancel()
		res, err := a.Runner.Execute(execCtx, p.Language, p.Code, p.Stdin, q.TimeLimitMS, q.MemoryLimitKB)
		if err != nil {
			fail(c, http.StatusBadGateway, "code runner unavailable: "+err.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"mode":          "custom",
			"compileOk":     res.CompileOK,
			"compileOutput": res.CompileOutput,
			"stdout":        res.Stdout,
			"stderr":        res.Stderr,
			"exitCode":      res.ExitCode,
			"timedOut":      res.TimedOut,
		})
		return
	}

	var samples []models.TestCase
	for _, tc := range q.TestCases {
		if !tc.Hidden {
			samples = append(samples, tc)
		}
		if len(samples) >= maxSampleRuns {
			break
		}
	}
	if len(samples) == 0 {
		fail(c, http.StatusBadRequest, "this question has no sample tests — use custom input")
		return
	}

	results := make([]runTestResult, 0, len(samples))
	passed := 0
	for i, tc := range samples {
		execCtx, cancel := context.WithTimeout(ctx, 55*time.Second)
		res, err := a.Runner.Execute(execCtx, p.Language, p.Code, tc.Input, q.TimeLimitMS, q.MemoryLimitKB)
		cancel()
		if err != nil {
			fail(c, http.StatusBadGateway, "code runner unavailable: "+err.Error())
			return
		}
		if !res.CompileOK {
			c.JSON(http.StatusOK, gin.H{
				"mode":          "samples",
				"compileOk":     false,
				"compileOutput": res.CompileOutput,
				"results":       []runTestResult{},
				"passed":        0,
				"total":         len(samples),
			})
			return
		}
		r := runTestResult{Index: i, Input: tc.Input, Expected: tc.Expected}
		switch {
		case res.TimedOut:
			r.Status = "timeout"
			r.Actual = res.Stdout
		case services.CompareOutput(res.Stdout, tc.Expected):
			r.Status = "passed"
			r.Passed = true
			passed++
		default:
			r.Status = "failed"
			r.Actual = res.Output
		}
		if len(r.Actual) > 2000 {
			r.Actual = r.Actual[:2000] + "\n… (truncated)"
		}
		results = append(results, r)
	}

	c.JSON(http.StatusOK, gin.H{
		"mode":      "samples",
		"compileOk": true,
		"results":   results,
		"passed":    passed,
		"total":     len(samples),
	})
}
