package wechat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"ai-sched-cli/internal/channel"
	"ai-sched-cli/internal/config"
)

const defaultBridgeURL = "http://127.0.0.1:18792"

type Sender struct {
	BridgeURL string
	StateFile string
}

type bridgeState struct {
	Users []struct {
		UserID    string `json:"userId"`
		SessionID string `json:"sessionId"`
		CWD       string `json:"cwd"`
	} `json:"users"`
	LastUserID    string `json:"lastUserId"`
	LastSessionID string `json:"lastSessionId"`
}

func (s Sender) Name() string {
	return "wechat"
}

func (s Sender) Check(ctx context.Context) error {
	state, err := s.loadState()
	if err != nil {
		return err
	}
	if !state.hasActiveUser() {
		return fmt.Errorf("wechat bridge has no active user in state file")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.bridgeURL()+"/send-wechat", nil)
	if err != nil {
		return fmt.Errorf("build wechat bridge request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("reach wechat bridge: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		return nil
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 500 {
		return nil
	}
	return fmt.Errorf("wechat bridge returned status %s", resp.Status)
}

func (s Sender) Send(ctx context.Context, msg channel.Message) (*channel.SendResult, error) {
	if err := s.Check(ctx); err != nil {
		return nil, err
	}

	payloadText := renderText(msg)
	if payloadText == "" {
		return nil, fmt.Errorf("wechat message is empty")
	}

	body, err := json.Marshal(map[string]string{
		"text": payloadText,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal wechat payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.bridgeURL()+"/send-wechat", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build wechat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send wechat request: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Success bool     `json:"success"`
		Sent    []string `json:"sent"`
		Error   string   `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode wechat response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if strings.TrimSpace(result.Error) != "" {
			return nil, fmt.Errorf("wechat bridge error: %s", strings.TrimSpace(result.Error))
		}
		return nil, fmt.Errorf("wechat bridge returned status %s", resp.Status)
	}
	if !result.Success {
		if strings.TrimSpace(result.Error) != "" {
			return nil, fmt.Errorf("wechat bridge error: %s", strings.TrimSpace(result.Error))
		}
		return nil, fmt.Errorf("wechat bridge reported unsuccessful send")
	}

	detail := "wechat delivered"
	if len(result.Sent) > 0 {
		detail = "wechat delivered: " + strings.Join(result.Sent, ", ")
	}
	return &channel.SendResult{
		Provider: s.Name(),
		Detail:   detail,
	}, nil
}

func renderText(msg channel.Message) string {
	title := strings.TrimSpace(msg.Title)
	body := strings.TrimSpace(msg.Body)
	priority := strings.TrimSpace(msg.Priority)

	var parts []string
	switch {
	case title != "":
		parts = append(parts, "任务通知")
		parts = append(parts, title)
	case body != "":
		parts = append(parts, "任务通知")
	default:
		return ""
	}

	if body != "" {
		parts = append(parts, "")
		parts = append(parts, body)
	}
	if label := priorityLabel(priority); label != "" {
		parts = append(parts, "")
		parts = append(parts, "优先级："+label)
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func priorityLabel(priority string) string {
	switch strings.ToLower(strings.TrimSpace(priority)) {
	case "", "normal":
		return ""
	case "high":
		return "高"
	case "low":
		return "低"
	default:
		return strings.TrimSpace(priority)
	}
}

func (s Sender) bridgeURL() string {
	bridgeURL := strings.TrimSpace(s.BridgeURL)
	if bridgeURL == "" {
		bridgeURL = defaultBridgeURL
	}
	return strings.TrimRight(bridgeURL, "/")
}

func (s Sender) loadState() (bridgeState, error) {
	statePath, err := config.ExpandPath(strings.TrimSpace(s.StateFile))
	if err != nil {
		return bridgeState{}, err
	}
	if statePath == "" {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return bridgeState{}, fmt.Errorf("resolve home dir: %w", homeErr)
		}
		statePath = filepath.Join(home, ".wechat-bridge-opencode", ".wechat-bridge-state.json")
	}

	data, err := os.ReadFile(statePath)
	if err != nil {
		return bridgeState{}, fmt.Errorf("read wechat state file %q: %w", statePath, err)
	}
	var state bridgeState
	if err := json.Unmarshal(data, &state); err != nil {
		return bridgeState{}, fmt.Errorf("parse wechat state file %q: %w", statePath, err)
	}
	return state, nil
}

func (s bridgeState) hasActiveUser() bool {
	if strings.TrimSpace(s.LastUserID) != "" || strings.TrimSpace(s.LastSessionID) != "" {
		return true
	}
	for _, user := range s.Users {
		if strings.TrimSpace(user.UserID) != "" || strings.TrimSpace(user.SessionID) != "" {
			return true
		}
	}
	return false
}
