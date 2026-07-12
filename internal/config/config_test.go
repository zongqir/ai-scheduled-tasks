package config

import "testing"

func TestValidateRejectsInvalidTimezone(t *testing.T) {
	cfg := Default()
	cfg.Timezone = "Mars/Olympus"

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid timezone error")
	}
}

func TestValidateRejectsDisabledDefaultChannel(t *testing.T) {
	cfg := Default()
	cfg.DefaultChannel = "webhook"
	cfg.Channels.Webhook.Enabled = false

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected disabled default channel error")
	}
}

func TestValidateAllowsEmptyDefaultChannel(t *testing.T) {
	cfg := Default()

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected empty default_channel to be allowed, got %v", err)
	}
}

func TestValidateRejectsUnsupportedDefaultChannel(t *testing.T) {
	cfg := Default()
	cfg.DefaultChannel = "slack"

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected unsupported default channel error")
	}
}

func TestValidateRejectsUnconfiguredDefaultChannel(t *testing.T) {
	cfg := Default()
	cfg.DefaultChannel = "webhook"
	cfg.Channels.Webhook.Enabled = true

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected unconfigured default channel error")
	}
}

func TestValidateRejectsDisabledTagRouteChannel(t *testing.T) {
	cfg := Default()
	cfg.TagRoutes = TagRoutes{
		"urgent": {
			{Channel: "wechat"},
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected disabled tag route channel error")
	}
}

func TestValidateRejectsUnconfiguredTagRouteChannel(t *testing.T) {
	cfg := Default()
	cfg.Channels.Webhook.Enabled = true
	cfg.TagRoutes = TagRoutes{
		"work": {
			{Channel: "webhook"},
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected unconfigured tag route channel error")
	}
}

func TestResolveTagRouteTargetsUnionsAndDeduplicates(t *testing.T) {
	cfg := Default()
	cfg.Channels.WeComRobot.Enabled = true
	cfg.Channels.WeComRobot.WebhookURL = "https://example.com/wecom"
	cfg.Channels.WeChat.Enabled = true
	cfg.TagRoutes = TagRoutes{
		"urgent": {
			{Channel: "wecom_robot"},
			{Channel: "wechat"},
		},
		"work": {
			{Channel: "wecom_robot"},
		},
	}

	targets := cfg.ResolveTagRouteTargets([]string{"work", "urgent"})
	if len(targets) != 2 {
		t.Fatalf("expected 2 route targets, got %d", len(targets))
	}
	if targets[0].Channel != "wecom_robot" {
		t.Fatalf("unexpected first target: %#v", targets[0])
	}
	if targets[1].Channel != "wechat" {
		t.Fatalf("unexpected second target: %#v", targets[1])
	}
}
