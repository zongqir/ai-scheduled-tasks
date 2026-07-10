package app

import (
	"fmt"
	"strings"

	"ai-sched-cli/internal/channel"
	"ai-sched-cli/internal/channel/dida"
	"ai-sched-cli/internal/channel/webhook"
	"ai-sched-cli/internal/channel/wechat"
	"ai-sched-cli/internal/channel/wecom"
	"ai-sched-cli/internal/config"
)

type channelFactory struct {
	config config.Config
}

func (f channelFactory) Resolve(name, ref string) (channel.Sender, error) {
	channelName := strings.TrimSpace(name)
	channelRef := strings.TrimSpace(ref)

	switch channelName {
	case "webhook":
		url := channelRef
		if url == "" {
			url = strings.TrimSpace(f.config.Channels.Webhook.URL)
		}
		return webhook.Sender{URL: url}, nil
	case "wecom_robot":
		webhookURL := channelRef
		if webhookURL == "" {
			webhookURL = strings.TrimSpace(f.config.Channels.WeComRobot.WebhookURL)
		}
		return wecom.Sender{WebhookURL: webhookURL}, nil
	case "dida":
		return dida.Sender{
			CLIPath:         strings.TrimSpace(f.config.Channels.Dida.CLIPath),
			DefaultProject:  strings.TrimSpace(f.config.Channels.Dida.DefaultProjectID),
			DefaultPriority: f.config.Channels.Dida.DefaultPriority,
			ProjectRef:      channelRef,
		}, nil
	case "wechat":
		return wechat.Sender{
			BridgeURL: strings.TrimSpace(f.config.Channels.WeChat.BridgeURL),
			StateFile: strings.TrimSpace(f.config.Channels.WeChat.StateFile),
		}, nil
	default:
		return nil, fmt.Errorf("unsupported channel: %s", channelName)
	}
}
