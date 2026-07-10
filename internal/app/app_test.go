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
	cfg.Channels.WeChat.Enabled = true
	cfg.TagRoutes = config.TagRoutes{
		"urgent": {
			{Channel: "wecom_robot"},
			{Channel: "wechat"},
		},
	}

	targets, err := resolveCreateChannels(cfg, nil, nil, []string{"urgent"})
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
	cfg.Channels.WeComRobot.Enabled = true
	cfg.TagRoutes = config.TagRoutes{
		"work": {
			{Channel: "wecom_robot"},
		},
	}

	targets, err := resolveCreateChannels(cfg, []string{"webhook"}, nil, []string{"work"})
	if err != nil {
		t.Fatalf("resolve create channels: %v", err)
	}
	if len(targets) != 1 || targets[0].Channel != "webhook" {
		t.Fatalf("unexpected explicit targets: %#v", targets)
	}
}
