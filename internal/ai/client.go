package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

type Client struct {
	Command    string
	Args       []string
	Agent      string
	Model      string
	Timeout    time.Duration
	MaxRetries int
}

type CreateTaskInput struct {
	RawInput       string
	Timezone       string
	CWD            string
	DefaultChannel string
	DefaultAgent   string
	Now            time.Time
}

type Health struct {
	Reachable bool
	Detail    string
}

func (c Client) CreateTask(ctx context.Context, input CreateTaskInput) (CreateTaskResponse, string, string, error) {
	prompt, err := buildCreateTaskPrompt(input)
	if err != nil {
		return CreateTaskResponse{}, "", "", err
	}

	var lastErr error
	attempts := c.MaxRetries + 1
	if attempts <= 0 {
		attempts = 1
	}

	for attempt := 0; attempt < attempts; attempt++ {
		rawOutput, requestErr := c.generate(ctx, prompt, input.CWD)
		if requestErr != nil {
			lastErr = requestErr
			continue
		}

		parsed, parseErr := parseCreateTaskResponse(rawOutput)
		if parseErr != nil {
			lastErr = parseErr
			continue
		}
		if validateErr := ValidateCreateTaskResponse(parsed); validateErr != nil {
			lastErr = validateErr
			continue
		}

		return parsed, prompt, rawOutput, nil
	}

	return CreateTaskResponse{}, prompt, "", lastErr
}

func (c Client) generate(ctx context.Context, prompt string, cwd string) (string, error) {
	if strings.TrimSpace(c.Command) == "" {
		return "", fmt.Errorf("ai command is required")
	}
	return runACPXCommand(ctx, c.Command, c.Args, c.Agent, c.Model, c.Timeout, plannerCWD(cwd), prompt)
}

func ValidateCreateTaskResponse(resp CreateTaskResponse) error {
	if strings.TrimSpace(resp.Action) != "create_task" {
		return fmt.Errorf("unsupported create_task action: %s", resp.Action)
	}
	if resp.RequiresConfirmation {
		if strings.TrimSpace(resp.Question) == "" {
			return fmt.Errorf("confirmation response requires question")
		}
		return validateTaskDraft(resp.DraftTask)
	}
	return validateTaskDraft(resp.Task)
}

func buildCreateTaskPrompt(input CreateTaskInput) (string, error) {
	loc, err := time.LoadLocation(strings.TrimSpace(input.Timezone))
	if err != nil {
		return "", fmt.Errorf("load timezone %q: %w", input.Timezone, err)
	}

	now := input.Now
	if now.IsZero() {
		now = time.Now()
	}

	payload, err := json.MarshalIndent(map[string]string{
		"raw_input":       strings.TrimSpace(input.RawInput),
		"timezone":        strings.TrimSpace(input.Timezone),
		"cwd":             strings.TrimSpace(input.CWD),
		"default_channel": strings.TrimSpace(input.DefaultChannel),
		"default_agent":   strings.TrimSpace(input.DefaultAgent),
		"now":             now.In(loc).Format(time.RFC3339),
	}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal create task input: %w", err)
	}

	return strings.TrimSpace(fmt.Sprintf(`
You are converting a natural-language scheduling request into a structured local task.

Return JSON only. Do not use markdown fences. Do not explain.

Valid schedule_type values:
- "once"
- "recurring"

Valid action value:
- "create_task"

If the request is ambiguous, return:
{
  "action": "create_task",
  "requires_confirmation": true,
  "question": "short clarification question",
  "draft_task": { ... }
}

If the request is clear, return:
{
  "action": "create_task",
  "requires_confirmation": false,
  "task": { ... }
}

Rules:
- Use the provided timezone
- Use RFC3339 for next_run_at
- For recurring tasks, include repeat_rule and time_of_day
- For once tasks, next_run_at must be the execution time
- Default action to "run_agent" unless the user explicitly wants only a notification
- For "run_agent", set "agent" to the configured default agent unless the user clearly specifies another one
- For "run_agent", include a concrete one-shot "instruction" that will be passed directly to acpx for execution at runtime
- Use the provided cwd unless the user explicitly asks for a different directory
- Use the provided default_channel unless the user clearly requests another channel
- Include "tags" when the user explicitly asks for labels/tags or category metadata
- summary should be short and concrete

Input:
%s
`, string(payload))), nil
}

func parseCreateTaskResponse(raw string) (CreateTaskResponse, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return CreateTaskResponse{}, fmt.Errorf("empty ai output")
	}

	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start < 0 || end < start {
		return CreateTaskResponse{}, fmt.Errorf("ai output does not contain JSON object")
	}

	var resp CreateTaskResponse
	if err := json.Unmarshal([]byte(trimmed[start:end+1]), &resp); err != nil {
		return CreateTaskResponse{}, fmt.Errorf("parse create task JSON: %w", err)
	}
	return resp, nil
}

func validateTaskDraft(draft TaskDraft) error {
	if strings.TrimSpace(draft.Summary) == "" {
		return fmt.Errorf("task summary is required")
	}
	action := strings.TrimSpace(draft.Action)
	if action == "" {
		action = "run_agent"
	}
	switch action {
	case "run_agent":
		if strings.TrimSpace(draft.Agent) == "" {
			return fmt.Errorf("run_agent task agent is required")
		}
		if strings.TrimSpace(draft.Instruction) == "" {
			return fmt.Errorf("run_agent task instruction is required")
		}
	case "notify":
		if strings.TrimSpace(draft.Instruction) == "" {
			return fmt.Errorf("notify task instruction is required")
		}
	default:
		return fmt.Errorf("unsupported task action: %s", draft.Action)
	}
	if strings.TrimSpace(draft.ScheduleType) == "" {
		return fmt.Errorf("task schedule_type is required")
	}
	if strings.TrimSpace(draft.Timezone) == "" {
		return fmt.Errorf("task timezone is required")
	}
	if strings.TrimSpace(draft.NextRunAt) == "" {
		return fmt.Errorf("task next_run_at is required")
	}
	if strings.TrimSpace(draft.CWD) == "" {
		return fmt.Errorf("task cwd is required")
	}
	if strings.TrimSpace(draft.Channel) == "" {
		return fmt.Errorf("task channel is required")
	}
	switch strings.TrimSpace(draft.ScheduleType) {
	case "once":
	case "recurring":
		if strings.TrimSpace(draft.RepeatRule) == "" {
			return fmt.Errorf("recurring task repeat_rule is required")
		}
		if strings.TrimSpace(draft.TimeOfDay) == "" {
			return fmt.Errorf("recurring task time_of_day is required")
		}
	default:
		return fmt.Errorf("unsupported schedule_type: %s", draft.ScheduleType)
	}
	return nil
}

func (c Client) Check(ctx context.Context) (Health, error) {
	if strings.TrimSpace(c.Command) == "" {
		return Health{}, fmt.Errorf("ai command is required")
	}
	agent := strings.TrimSpace(c.Agent)
	if agent == "" {
		agent = "codex"
	}
	command := strings.TrimSpace(c.Command)
	args := append([]string(nil), c.Args...)
	if !containsArg(args, "--timeout") && c.Timeout > 0 {
		args = append(args, "--timeout", fmt.Sprintf("%.0f", c.Timeout.Seconds()))
	}
	statusArgs := append(args, agent, "status")
	cmd := exec.CommandContext(ctx, command, statusArgs...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return Health{Reachable: false, Detail: detail}, nil
	}
	detail := strings.TrimSpace(stdout.String())
	if detail == "" {
		detail = "status ok"
	}
	return Health{Reachable: true, Detail: detail}, nil
}

func plannerCWD(cwd string) string {
	if strings.TrimSpace(cwd) == "" {
		return "."
	}
	return cwd
}

func runACPXCommand(ctx context.Context, command string, args []string, agent string, model string, timeout time.Duration, cwd string, prompt string) (string, error) {
	baseArgs := append([]string(nil), args...)
	if strings.TrimSpace(agent) == "" {
		agent = "codex"
	}
	if strings.TrimSpace(model) != "" && !containsArg(baseArgs, "--model") {
		baseArgs = append(baseArgs, "--model", strings.TrimSpace(model))
	}
	if !containsArg(baseArgs, "--max-turns") {
		baseArgs = append(baseArgs, "--max-turns", "1")
	}
	if timeout > 0 && !containsArg(baseArgs, "--timeout") {
		baseArgs = append(baseArgs, "--timeout", fmt.Sprintf("%.0f", timeout.Seconds()))
	}
	cmdArgs := append(baseArgs, agent, "exec", prompt)
	cmd := exec.CommandContext(ctx, command, cmdArgs...)
	cmd.Dir = cwd

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	output := strings.TrimSpace(stdout.String())
	if err == nil {
		return output, nil
	}
	errText := strings.TrimSpace(stderr.String())
	if errText == "" {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				errText = fmt.Sprintf("command exited with code %d", status.ExitStatus())
			}
		}
	}
	if errText == "" {
		errText = err.Error()
	}
	return output, fmt.Errorf("%s", errText)
}

func containsArg(args []string, target string) bool {
	for _, arg := range args {
		if arg == target {
			return true
		}
	}
	return false
}
