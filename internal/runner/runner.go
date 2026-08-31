package runner

import "context"

// Runner executes untrusted student code in a sandbox.
type Runner interface {
	// Execute compiles and runs code (python | java | c) with the given stdin.
	Execute(ctx context.Context, language, code, stdin string, timeLimitMS, memoryKB int) (*ExecResult, error)
	Name() string
}
