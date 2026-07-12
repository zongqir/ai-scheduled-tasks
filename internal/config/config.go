package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const fileName = "config.json"

type Config struct {
	Timezone       string         `json:"timezone"`
	DatabasePath   string         `json:"database_path"`
	DefaultChannel string         `json:"default_channel"`
	AI             AIConfig       `json:"ai"`
	Daemon         DaemonConfig   `json:"daemon"`
	Channels       ChannelConfigs `json:"channels"`
	TagRoutes      TagRoutes      `json:"tag_routes"`
}

type AIConfig struct {
	Command        string   `json:"command"`
	Args           []string `json:"args"`
	Agent          string   `json:"agent"`
	Model          string   `json:"model"`
	TimeoutSeconds int      `json:"timeout_seconds"`
	MaxRetries     int      `json:"max_retries"`
}

type DaemonConfig struct {
	PollIntervalSeconds        int `json:"poll_interval_seconds"`
	NotificationPollSeconds    int `json:"notification_poll_seconds"`
	StuckRunningTimeoutSeconds int `json:"stuck_running_timeout_seconds"`
	NotificationMaxRetries     int `json:"notification_max_retries"`
}

type ChannelConfigs struct {
	Webhook    WebhookConfig `json:"webhook"`
	WeComRobot WeComConfig   `json:"wecom_robot"`
	Dida       DidaConfig    `json:"dida"`
	WeChat     WeChatConfig  `json:"wechat"`
}

type WebhookConfig struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url"`
}

type WeComConfig struct {
	Enabled    bool   `json:"enabled"`
	WebhookURL string `json:"webhook_url"`
}

type DidaConfig struct {
	Enabled          bool   `json:"enabled"`
	CLIPath          string `json:"cli_path"`
	DefaultProjectID string `json:"default_project_id"`
	DefaultPriority  int    `json:"default_priority"`
}

type WeChatConfig struct {
	Enabled   bool   `json:"enabled"`
	BridgeURL string `json:"bridge_url"`
	StateFile string `json:"state_file"`
}

func Default() Config {
	return Config{
		Timezone:       "Asia/Shanghai",
		DatabasePath:   "~/.local/share/ai-sched-cli/tasks.db",
		DefaultChannel: "",
		AI: AIConfig{
			Command:        "acpx",
			Args:           []string{},
			Agent:          "codex",
			TimeoutSeconds: 15,
			MaxRetries:     1,
		},
		Daemon: DaemonConfig{
			PollIntervalSeconds:        1,
			NotificationPollSeconds:    10,
			StuckRunningTimeoutSeconds: 300,
			NotificationMaxRetries:     10,
		},
		Channels: ChannelConfigs{
			Webhook: WebhookConfig{
				Enabled: false,
			},
			WeComRobot: WeComConfig{
				Enabled: false,
			},
			Dida: DidaConfig{
				Enabled:         false,
				CLIPath:         "dida365",
				DefaultPriority: 3,
			},
			WeChat: WeChatConfig{
				Enabled: false,
			},
		},
	}
}

func DefaultPath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		base = filepath.Join(home, ".config")
	}

	return filepath.Join(base, "ai-sched-cli", fileName), nil
}

func ExpandPath(path string) (string, error) {
	if path == "" {
		return "", nil
	}

	expanded := os.ExpandEnv(path)
	if strings.HasPrefix(expanded, "~/") || expanded == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		if expanded == "~" {
			expanded = home
		} else {
			expanded = filepath.Join(home, expanded[2:])
		}
	}

	return filepath.Clean(expanded), nil
}

func Load(path string) (Config, error) {
	resolved, err := ExpandPath(path)
	if err != nil {
		return Config{}, err
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	cfg := Default()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}

	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func Save(path string, cfg Config) error {
	resolved, err := ExpandPath(path)
	if err != nil {
		return err
	}

	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(resolved, data, 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	return nil
}

func Ensure(path string) (Config, bool, error) {
	resolved, err := ExpandPath(path)
	if err != nil {
		return Config{}, false, err
	}

	if _, err := os.Stat(resolved); err == nil {
		cfg, loadErr := Load(resolved)
		return cfg, false, loadErr
	} else if !os.IsNotExist(err) {
		return Config{}, false, fmt.Errorf("stat config: %w", err)
	}

	cfg := Default()
	if err := Save(resolved, cfg); err != nil {
		return Config{}, false, err
	}
	return cfg, true, nil
}

func (c Config) ResolvedDatabasePath() (string, error) {
	return ExpandPath(c.DatabasePath)
}

func (c Config) EnabledChannels() []string {
	var channels []string
	if c.Channels.Webhook.Enabled {
		channels = append(channels, "webhook")
	}
	if c.Channels.WeComRobot.Enabled {
		channels = append(channels, "wecom_robot")
	}
	if c.Channels.Dida.Enabled {
		channels = append(channels, "dida")
	}
	if c.Channels.WeChat.Enabled {
		channels = append(channels, "wechat")
	}
	return channels
}

func (c Config) Validate() error {
	if c.Timezone == "" {
		return fmt.Errorf("timezone is required")
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		return fmt.Errorf("invalid timezone %q: %w", c.Timezone, err)
	}
	if c.DatabasePath == "" {
		return fmt.Errorf("database_path is required")
	}
	if strings.TrimSpace(c.DefaultChannel) != "" {
		if !isSupportedChannel(c.DefaultChannel) {
			return fmt.Errorf("unsupported default_channel: %s", c.DefaultChannel)
		}
		if err := c.ValidateChannelTarget(c.DefaultChannel, ""); err != nil {
			return fmt.Errorf("default_channel %q is invalid: %w", c.DefaultChannel, err)
		}
	}
	if c.Daemon.PollIntervalSeconds <= 0 {
		return fmt.Errorf("daemon.poll_interval_seconds must be > 0")
	}
	if c.Daemon.NotificationPollSeconds <= 0 {
		return fmt.Errorf("daemon.notification_poll_seconds must be > 0")
	}
	if c.Daemon.StuckRunningTimeoutSeconds <= 0 {
		return fmt.Errorf("daemon.stuck_running_timeout_seconds must be > 0")
	}
	if c.Daemon.NotificationMaxRetries <= 0 {
		return fmt.Errorf("daemon.notification_max_retries must be > 0")
	}
	if c.AI.TimeoutSeconds <= 0 {
		return fmt.Errorf("ai.timeout_seconds must be > 0")
	}
	if strings.TrimSpace(c.AI.Command) == "" {
		return fmt.Errorf("ai.command is required")
	}
	if strings.TrimSpace(c.AI.Agent) == "" {
		return fmt.Errorf("ai.agent is required")
	}
	if c.AI.MaxRetries < 0 {
		return fmt.Errorf("ai.max_retries must be >= 0")
	}
	if err := c.validateTagRoutes(); err != nil {
		return err
	}
	return nil
}

func (c *Config) applyDefaults() {
	def := Default()

	if c.Timezone == "" {
		c.Timezone = def.Timezone
	}
	if c.DatabasePath == "" {
		c.DatabasePath = def.DatabasePath
	}
	if c.DefaultChannel == "" {
		c.DefaultChannel = def.DefaultChannel
	}
	if c.AI.Command == "" {
		c.AI.Command = def.AI.Command
	}
	if len(c.AI.Args) == 0 {
		c.AI.Args = append([]string(nil), def.AI.Args...)
	}
	if c.AI.Agent == "" {
		c.AI.Agent = def.AI.Agent
	}
	if c.AI.Model == "" {
		c.AI.Model = def.AI.Model
	}
	if c.AI.TimeoutSeconds == 0 {
		c.AI.TimeoutSeconds = def.AI.TimeoutSeconds
	}
	if c.Daemon.PollIntervalSeconds == 0 {
		c.Daemon.PollIntervalSeconds = def.Daemon.PollIntervalSeconds
	}
	if c.Daemon.NotificationPollSeconds == 0 {
		c.Daemon.NotificationPollSeconds = def.Daemon.NotificationPollSeconds
	}
	if c.Daemon.StuckRunningTimeoutSeconds == 0 {
		c.Daemon.StuckRunningTimeoutSeconds = def.Daemon.StuckRunningTimeoutSeconds
	}
	if c.Daemon.NotificationMaxRetries == 0 {
		c.Daemon.NotificationMaxRetries = def.Daemon.NotificationMaxRetries
	}
	if c.Channels.Dida.CLIPath == "" {
		c.Channels.Dida.CLIPath = def.Channels.Dida.CLIPath
	}
	if c.Channels.Dida.DefaultPriority == 0 {
		c.Channels.Dida.DefaultPriority = def.Channels.Dida.DefaultPriority
	}
	c.applyTagRouteDefaults()
}

func (c Config) isChannelEnabled(name string) bool {
	switch name {
	case "webhook":
		return c.Channels.Webhook.Enabled
	case "wecom_robot":
		return c.Channels.WeComRobot.Enabled
	case "dida":
		return c.Channels.Dida.Enabled
	case "wechat":
		return c.Channels.WeChat.Enabled
	default:
		return false
	}
}

func (c Config) ValidateChannelTarget(name, ref string) error {
	channelName := strings.TrimSpace(name)
	channelRef := strings.TrimSpace(ref)
	if channelName == "" {
		return fmt.Errorf("channel cannot be empty")
	}
	if !isSupportedChannel(channelName) {
		return fmt.Errorf("unsupported channel: %s", channelName)
	}
	if !c.isChannelEnabled(channelName) {
		return fmt.Errorf("channel %q is disabled", channelName)
	}

	switch channelName {
	case "webhook":
		if channelRef == "" && strings.TrimSpace(c.Channels.Webhook.URL) == "" {
			return fmt.Errorf("channel %q requires channels.webhook.url or channel_ref", channelName)
		}
	case "wecom_robot":
		if channelRef == "" && strings.TrimSpace(c.Channels.WeComRobot.WebhookURL) == "" {
			return fmt.Errorf("channel %q requires channels.wecom_robot.webhook_url or channel_ref", channelName)
		}
	}
	return nil
}

func (c Config) ChannelConfigWarnings() []string {
	var warnings []string
	if c.Channels.Webhook.Enabled && strings.TrimSpace(c.Channels.Webhook.URL) == "" {
		warnings = append(warnings, "channel webhook is enabled but url is empty")
	}
	if c.Channels.WeComRobot.Enabled && strings.TrimSpace(c.Channels.WeComRobot.WebhookURL) == "" {
		warnings = append(warnings, "channel wecom_robot is enabled but webhook_url is empty")
	}
	return warnings
}

func isSupportedChannel(name string) bool {
	switch name {
	case "webhook", "wecom_robot", "dida", "wechat":
		return true
	default:
		return false
	}
}
