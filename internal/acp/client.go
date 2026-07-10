package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type Client struct {
	Command string
	Args    []string
	Timeout time.Duration
}

type Health struct {
	Reachable bool
	Detail    string
}

func (c Client) Check(ctx context.Context) (Health, error) {
	session, err := c.start(ctx)
	if err != nil {
		return Health{}, err
	}
	defer session.close()

	if _, err := session.request(ctx, 1, "initialize", map[string]any{
		"protocolVersion":    1,
		"clientCapabilities": map[string]any{},
		"clientInfo": map[string]any{
			"name":    "ai-sched-cli",
			"version": "dev",
		},
	}); err != nil {
		return Health{}, err
	}

	return Health{
		Reachable: true,
		Detail:    fmt.Sprintf("acp ready: %s %s", c.Command, strings.Join(c.Args, " ")),
	}, nil
}

func (c Client) Prompt(ctx context.Context, cwd string, prompt string) (string, error) {
	session, err := c.start(ctx)
	if err != nil {
		return "", err
	}
	defer session.close()

	if _, err := session.request(ctx, 1, "initialize", map[string]any{
		"protocolVersion":    1,
		"clientCapabilities": map[string]any{},
		"clientInfo": map[string]any{
			"name":    "ai-sched-cli",
			"version": "dev",
		},
	}); err != nil {
		return "", err
	}

	newResult, err := session.request(ctx, 2, "session/new", map[string]any{
		"cwd":        cwd,
		"mcpServers": []any{},
	})
	if err != nil {
		return "", err
	}

	sessionID, _ := newResult["sessionId"].(string)
	if strings.TrimSpace(sessionID) == "" {
		return "", fmt.Errorf("acp session/new did not return sessionId")
	}

	var output strings.Builder
	if _, err := session.requestWithUpdates(ctx, 3, "session/prompt", map[string]any{
		"sessionId": sessionID,
		"prompt": []map[string]string{
			{"type": "text", "text": prompt},
		},
	}, func(method string, params map[string]any) {
		if method != "session/update" {
			return
		}
		update, _ := params["update"].(map[string]any)
		if update == nil {
			return
		}
		if update["sessionUpdate"] != "agent_message_chunk" {
			return
		}
		content, _ := update["content"].(map[string]any)
		if content == nil {
			return
		}
		if content["type"] != "text" {
			return
		}
		text, _ := content["text"].(string)
		output.WriteString(text)
	}); err != nil {
		return "", err
	}

	return strings.TrimSpace(output.String()), nil
}

func (c Client) start(ctx context.Context) (*session, error) {
	if strings.TrimSpace(c.Command) == "" {
		return nil, fmt.Errorf("acp command is required")
	}

	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)

	cmd := exec.CommandContext(runCtx, c.Command, c.Args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("acp stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		cancel()
		return nil, fmt.Errorf("acp stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		cancel()
		return nil, fmt.Errorf("start acp command: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	return &session{
		cmd:     cmd,
		cancel:  cancel,
		stdin:   stdin,
		scanner: scanner,
	}, nil
}

type session struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	stdin  interface {
		Write([]byte) (int, error)
		Close() error
	}
	scanner *bufio.Scanner
}

func (s *session) close() {
	_ = s.stdin.Close()
	s.cancel()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	_ = s.cmd.Wait()
}

func (s *session) request(ctx context.Context, id int, method string, params map[string]any) (map[string]any, error) {
	return s.requestWithUpdates(ctx, id, method, params, nil)
}

func (s *session) requestWithUpdates(ctx context.Context, id int, method string, params map[string]any, onUpdate func(method string, params map[string]any)) (map[string]any, error) {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal acp request: %w", err)
	}
	if _, err := s.stdin.Write(append(data, '\n')); err != nil {
		return nil, fmt.Errorf("write acp request: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		if !s.scanner.Scan() {
			if err := s.scanner.Err(); err != nil {
				return nil, fmt.Errorf("read acp response: %w", err)
			}
			return nil, fmt.Errorf("acp process exited before responding to %s", method)
		}

		line := strings.TrimSpace(s.scanner.Text())
		if line == "" {
			continue
		}

		var message map[string]any
		if err := json.Unmarshal([]byte(line), &message); err != nil {
			continue
		}

		if msgID, ok := message["id"].(float64); ok && int(msgID) == id {
			if errPayload, ok := message["error"].(map[string]any); ok {
				return nil, fmt.Errorf("acp %s error: %v", method, errPayload["message"])
			}
			result, _ := message["result"].(map[string]any)
			if result == nil {
				return map[string]any{}, nil
			}
			return result, nil
		}

		if onUpdate != nil {
			msgMethod, _ := message["method"].(string)
			msgParams, _ := message["params"].(map[string]any)
			onUpdate(msgMethod, msgParams)
		}
	}
}
