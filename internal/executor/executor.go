package executor

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

type Action string

const (
	ActionNotify   Action = "notify"
	ActionRunAgent Action = "run_agent"
)

type Runner interface {
	RunAgent(ctx context.Context, input RunAgentInput) (Result, error)
}

type ACPXRunner struct {
	Command string
	Args    []string
}

type RunAgentInput struct {
	CWD        string
	Agent      string
	Prompt     string
	Model      string
	Timeout    time.Duration
	ApproveAll bool
	ExtraArgs  []string
}

type Result struct {
	Command  []string
	Stdout   string
	Stderr   string
	ExitCode int
}

func Validate(action string) error {
	switch Action(strings.TrimSpace(action)) {
	case ActionNotify, ActionRunAgent:
		return nil
	default:
		return fmt.Errorf("unsupported action: %s", action)
	}
}

func (r ACPXRunner) RunAgent(ctx context.Context, input RunAgentInput) (Result, error) {
	if strings.TrimSpace(input.CWD) == "" {
		return Result{}, fmt.Errorf("cwd is required")
	}
	if strings.TrimSpace(input.Agent) == "" {
		return Result{}, fmt.Errorf("agent is required")
	}
	if strings.TrimSpace(input.Prompt) == "" {
		return Result{}, fmt.Errorf("prompt is required")
	}

	timeout := input.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}

	command := strings.TrimSpace(r.Command)
	if command == "" {
		command = "acpx"
	}

	args := append([]string(nil), r.Args...)
	if input.ApproveAll && !containsArg(args, "--approve-all") {
		args = append(args, "--approve-all")
	}
	if !containsArg(args, "--max-turns") {
		args = append(args, "--max-turns", "1")
	}
	if timeout > 0 && !containsArg(args, "--timeout") {
		args = append(args, "--timeout", fmt.Sprintf("%.0f", timeout.Seconds()))
	}
	if strings.TrimSpace(input.Model) != "" && !containsArg(args, "--model") {
		args = append(args, "--model", strings.TrimSpace(input.Model))
	}
	args = append(args, input.ExtraArgs...)
	args = append(args, strings.TrimSpace(input.Agent), "exec")
	args = append(args, strings.TrimSpace(input.Prompt))

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = input.CWD

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := Result{
		Command:  append([]string{command}, args...),
		Stdout:   strings.TrimSpace(stdout.String()),
		Stderr:   strings.TrimSpace(stderr.String()),
		ExitCode: 0,
	}
	if err == nil {
		return result, nil
	}

	result.ExitCode = -1
	if exitErr, ok := err.(*exec.ExitError); ok {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			result.ExitCode = status.ExitStatus()
		}
	}
	if result.Stderr == "" {
		result.Stderr = err.Error()
	}
	return result, err
}

func containsArg(args []string, target string) bool {
	for _, arg := range args {
		if arg == target {
			return true
		}
	}
	return false
}
