package app

import (
	"testing"

	"ai-sched-cli/internal/config"
)

func TestResolveTaskChannelsUsesDefaultAndAlignsRefs(t *testing.T) {
	targets, err := resolveTaskChannels(nil, []string{"team-a"}, "wecom_robot")
	if err != nil {
		t.Fatalf("resolve task channels: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(targets))
	}
	if targets[0].Channel != "wecom_robot" || targets[0].ChannelRef != "team-a" {
		t.Fatalf("unexpected target: %#v", targets[0])
	}
}

func TestResolveTaskChannelsRejectsExtraRefs(t *testing.T) {
	_, err := resolveTaskChannels([]string{"wechat"}, []string{"a", "b"}, "")
	if err == nil {
		t.Fatal("expected extra refs to fail")
	}
}

func TestResolveCreateChannelsUsesTagRoutesBeforeDefault(t *testing.T) {
	cfg := config.Default()
	cfg.Channels.WeComRobot.Enabled = true
	cfg.Channels.WeComRobot.WebhookURL = "https://example.com/wecom"
	cfg.Channels.WeChat.Enabled = true
	cfg.TagRoutes = config.TagRoutes{
		"urgent": {
			{Channel: "wecom_robot"},
			{Channel: "wechat"},
		},
	}

	targets, err := resolveCreateChannels(cfg, nil, nil, []string{"urgent"}, true)
	if err != nil {
		t.Fatalf("resolve create channels: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(targets))
	}
	if targets[0].Channel != "wecom_robot" || targets[1].Channel != "wechat" {
		t.Fatalf("unexpected targets: %#v", targets)
	}
}

func TestResolveCreateChannelsExplicitWinsOverTagRoutes(t *testing.T) {
	cfg := config.Default()
	cfg.Channels.Webhook.Enabled = true
	cfg.Channels.WeComRobot.WebhookURL = "https://example.com/wecom"
	cfg.Channels.WeComRobot.Enabled = true
	cfg.TagRoutes = config.TagRoutes{
		"work": {
			{Channel: "wecom_robot"},
		},
	}

	targets, err := resolveCreateChannels(cfg, []string{"webhook"}, []string{"https://example.com/webhook"}, []string{"work"}, true)
	if err != nil {
		t.Fatalf("resolve create channels: %v", err)
	}
	if len(targets) != 1 || targets[0].Channel != "webhook" {
		t.Fatalf("unexpected explicit targets: %#v", targets)
	}
}

func TestResolveCreateChannelsRejectsNotifyTasksWithoutConfiguredRoute(t *testing.T) {
	cfg := config.Default()

	_, err := resolveCreateChannels(cfg, nil, nil, []string{"work"}, true)
	if err == nil {
		t.Fatal("expected missing notification route to fail")
	}
}

func TestResolveCreateChannelsAcceptsConfiguredDefaultChannel(t *testing.T) {
	cfg := config.Default()
	cfg.DefaultChannel = "webhook"
	cfg.Channels.Webhook.Enabled = true
	cfg.Channels.Webhook.URL = "https://example.com/webhook"

	targets, err := resolveCreateChannels(cfg, nil, nil, nil, true)
	if err != nil {
		t.Fatalf("resolve configured default channel: %v", err)
	}
	if len(targets) != 1 || targets[0].Channel != "webhook" {
		t.Fatalf("unexpected default targets: %#v", targets)
	}
}

func TestResolveCreateChannelsAllowsSilentTasks(t *testing.T) {
	cfg := config.Default()

	targets, err := resolveCreateChannels(cfg, nil, nil, []string{"work"}, false)
	if err != nil {
		t.Fatalf("resolve silent channels: %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("expected no channels, got %#v", targets)
	}
}
