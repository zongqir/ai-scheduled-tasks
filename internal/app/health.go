package app

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"ai-sched-cli/internal/ai"
	"ai-sched-cli/internal/channel/dida"
	"ai-sched-cli/internal/channel/wechat"
	"ai-sched-cli/internal/config"
)

type healthCheck struct {
	Name   string
	Status string
	Detail string
}

func collectHealthChecks(ctx context.Context, cfg config.Config) []healthCheck {
	var checks []healthCheck

	aiClient := ai.Client{
		Command: cfg.AI.Command,
		Args:    append([]string(nil), cfg.AI.Args...),
		Agent:   cfg.AI.Agent,
		Model:   cfg.AI.Model,
		Timeout: minDuration(time.Duration(cfg.AI.TimeoutSeconds)*time.Second, 5*time.Second),
	}
	if health, err := aiClient.Check(ctx); err != nil {
		checks = append(checks, healthCheck{Name: "ai", Status: "fail", Detail: err.Error()})
	} else {
		status := "warn"
		if health.Reachable {
			status = "ok"
		}
		checks = append(checks, healthCheck{Name: "ai", Status: status, Detail: health.Detail})
	}

	checks = append(checks, checkWebhookHealth(ctx, cfg)...)
	checks = append(checks, checkWeComHealth(ctx, cfg)...)
	checks = append(checks, checkDidaHealth(cfg)...)
	checks = append(checks, checkWeChatHealth(cfg)...)
	return checks
}

func checkWebhookHealth(ctx context.Context, cfg config.Config) []healthCheck {
	if !cfg.Channels.Webhook.Enabled {
		return []healthCheck{{Name: "channel:webhook", Status: "disabled", Detail: "disabled"}}
	}

	webhookURL := strings.TrimSpace(cfg.Channels.Webhook.URL)
	if webhookURL == "" {
		return []healthCheck{{Name: "channel:webhook", Status: "warn", Detail: "enabled but url is empty"}}
	}
	return []healthCheck{httpHealthCheck(ctx, "channel:webhook", webhookURL)}
}

func checkWeComHealth(ctx context.Context, cfg config.Config) []healthCheck {
	if !cfg.Channels.WeComRobot.Enabled {
		return []healthCheck{{Name: "channel:wecom_robot", Status: "disabled", Detail: "disabled"}}
	}

	webhookURL := strings.TrimSpace(cfg.Channels.WeComRobot.WebhookURL)
	if webhookURL == "" {
		return []healthCheck{{Name: "channel:wecom_robot", Status: "warn", Detail: "enabled but webhook_url is empty"}}
	}
	return []healthCheck{httpHealthCheck(ctx, "channel:wecom_robot", webhookURL)}
}

func checkDidaHealth(cfg config.Config) []healthCheck {
	if !cfg.Channels.Dida.Enabled {
		return []healthCheck{{Name: "channel:dida", Status: "disabled", Detail: "disabled"}}
	}

	sender := dida.Sender{
		CLIPath:         strings.TrimSpace(cfg.Channels.Dida.CLIPath),
		DefaultProject:  strings.TrimSpace(cfg.Channels.Dida.DefaultProjectID),
		DefaultPriority: cfg.Channels.Dida.DefaultPriority,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sender.Check(ctx); err != nil {
		return []healthCheck{{Name: "channel:dida", Status: "fail", Detail: err.Error()}}
	}
	return []healthCheck{{Name: "channel:dida", Status: "ok", Detail: "project list reachable"}}
}

func checkWeChatHealth(cfg config.Config) []healthCheck {
	if !cfg.Channels.WeChat.Enabled {
		return []healthCheck{{Name: "channel:wechat", Status: "disabled", Detail: "disabled"}}
	}

	sender := wechat.Sender{
		BridgeURL: strings.TrimSpace(cfg.Channels.WeChat.BridgeURL),
		StateFile: strings.TrimSpace(cfg.Channels.WeChat.StateFile),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sender.Check(ctx); err != nil {
		return []healthCheck{{Name: "channel:wechat", Status: "fail", Detail: err.Error()}}
	}
	return []healthCheck{{Name: "channel:wechat", Status: "ok", Detail: "wechat bridge reachable with active user"}}
}

func httpHealthCheck(ctx context.Context, name, rawURL string) healthCheck {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return healthCheck{Name: name, Status: "fail", Detail: fmt.Sprintf("parse url: %v", err)}
	}
	if parsedURL.Scheme == "" || parsedURL.Host == "" {
		return healthCheck{Name: name, Status: "fail", Detail: "invalid url"}
	}

	reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodHead, rawURL, nil)
	if err != nil {
		return healthCheck{Name: name, Status: "fail", Detail: fmt.Sprintf("build request: %v", err)}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return healthCheck{Name: name, Status: "fail", Detail: fmt.Sprintf("request failed: %v", err)}
	}
	defer resp.Body.Close()

	return healthCheck{Name: name, Status: "ok", Detail: resp.Status}
}

func minDuration(a, b time.Duration) time.Duration {
	if a <= 0 {
		return b
	}
	if a < b {
		return a
	}
	return b
}
