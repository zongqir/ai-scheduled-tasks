package wecom

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"ai-sched-cli/internal/channel"
)

type Sender struct {
	WebhookURL string
}

func (s Sender) Name() string {
	return "wecom_robot"
}

func (s Sender) Check(context.Context) error {
	if strings.TrimSpace(s.WebhookURL) == "" {
		return fmt.Errorf("wecom webhook url is empty")
	}
	return nil
}

func (s Sender) Send(ctx context.Context, msg channel.Message) (*channel.SendResult, error) {
	content := strings.TrimSpace(msg.Body)
	if strings.TrimSpace(msg.Title) != "" {
		content = strings.TrimSpace(msg.Title) + "\n" + content
	}

	body, err := json.Marshal(map[string]any{
		"msgtype": "text",
		"text": map[string]string{
			"content": content,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal wecom payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.WebhookURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("build wecom request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send wecom request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("wecom returned status %s", resp.Status)
	}

	return &channel.SendResult{
		Provider: s.Name(),
		Detail:   fmt.Sprintf("wecom delivered: %s", resp.Status),
	}, nil
}
