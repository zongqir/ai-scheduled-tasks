package dida

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"ai-sched-cli/internal/channel"
)

type Sender struct {
	CLIPath         string
	DefaultProject  string
	DefaultPriority int
	ProjectRef      string
}

type project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s Sender) Name() string {
	return "dida"
}

func (s Sender) Check(ctx context.Context) error {
	cliPath, err := s.resolveCLIPath()
	if err != nil {
		return err
	}

	_, err = s.run(ctx, cliPath, "project", "list", "--json")
	if err != nil {
		return fmt.Errorf("check dida project list: %w", err)
	}
	return nil
}

func (s Sender) Send(ctx context.Context, msg channel.Message) (*channel.SendResult, error) {
	cliPath, err := s.resolveCLIPath()
	if err != nil {
		return nil, err
	}

	args := []string{"task", "create", strings.TrimSpace(msg.Title)}
	projectID, err := s.resolveProjectID(ctx, cliPath)
	if err != nil {
		return nil, err
	}
	if projectID != "" {
		args = append(args, "-p", projectID)
	}

	content := buildContent(msg)
	if content != "" {
		args = append(args, "-c", content)
	}

	priority := s.DefaultPriority
	if priority <= 0 {
		priority = 3
	}
	args = append(args, "--priority", fmt.Sprintf("%d", priority), "-j")

	stdout, err := s.run(ctx, cliPath, args...)
	if err != nil {
		return nil, fmt.Errorf("create dida task: %w", err)
	}

	return &channel.SendResult{
		Provider: s.Name(),
		Detail:   fmt.Sprintf("dida task created%s", projectSuffix(projectID, stdout)),
	}, nil
}

func (s Sender) resolveCLIPath() (string, error) {
	cliPath := strings.TrimSpace(s.CLIPath)
	if cliPath == "" {
		cliPath = "dida365"
	}
	resolved, err := exec.LookPath(cliPath)
	if err != nil {
		return "", fmt.Errorf("find dida cli %q: %w", cliPath, err)
	}
	return resolved, nil
}

func (s Sender) resolveProjectID(ctx context.Context, cliPath string) (string, error) {
	projectRef := strings.TrimSpace(s.ProjectRef)
	if projectRef == "" {
		projectRef = strings.TrimSpace(s.DefaultProject)
	}
	if projectRef == "" {
		return "", nil
	}

	stdout, err := s.run(ctx, cliPath, "project", "list", "--json")
	if err != nil {
		return "", fmt.Errorf("list dida projects: %w", err)
	}

	var projects []project
	if err := json.Unmarshal([]byte(stdout), &projects); err != nil {
		return "", fmt.Errorf("parse dida projects: %w", err)
	}

	var nameMatches []project
	for _, candidate := range projects {
		if candidate.ID == projectRef {
			return candidate.ID, nil
		}
		if candidate.Name == projectRef {
			nameMatches = append(nameMatches, candidate)
		}
	}

	switch len(nameMatches) {
	case 0:
		return "", fmt.Errorf("dida project not found: %s", projectRef)
	case 1:
		return nameMatches[0].ID, nil
	default:
		return "", fmt.Errorf("multiple dida projects match %q", projectRef)
	}
}

func (s Sender) run(ctx context.Context, cliPath string, args ...string) (string, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, cliPath, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if strings.TrimSpace(stderr.String()) != "" {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

func buildContent(msg channel.Message) string {
	body := strings.TrimSpace(msg.Body)
	if body == "" {
		return ""
	}
	if strings.TrimSpace(msg.Priority) == "" {
		return body
	}
	return body + "\n\nPriority: " + strings.TrimSpace(msg.Priority)
}

func projectSuffix(projectID, stdout string) string {
	if projectID == "" {
		return ""
	}
	if strings.TrimSpace(stdout) == "" {
		return fmt.Sprintf(" in project %s", projectID)
	}
	return fmt.Sprintf(" in project %s", projectID)
}
