package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"ai-sched-cli/internal/channel"
)

type Sender struct {
	URL string
}

func (s Sender) Name() string {
	return "webhook"
}

func (s Sender) Check(context.Context) error {
	if strings.TrimSpace(s.URL) == "" {
		return fmt.Errorf("webhook url is empty")
	}
	return nil
}

func (s Sender) Send(ctx context.Context, msg channel.Message) (*channel.SendResult, error) {
	body, err := json.Marshal(map[string]string{
		"title":    msg.Title,
		"body":     msg.Body,
		"priority": msg.Priority,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal webhook payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.URL, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("build webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send webhook request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("webhook returned status %s", resp.Status)
	}

	return &channel.SendResult{
		Provider: s.Name(),
		Detail:   fmt.Sprintf("webhook delivered: %s", resp.Status),
	}, nil
}
