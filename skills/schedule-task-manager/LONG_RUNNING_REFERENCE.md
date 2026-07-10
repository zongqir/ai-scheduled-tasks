# Long-Running Reference

Use this reference only when the user explicitly asks for:

- long-running setup
- boot startup
- background service management
- stable server deployment

Do not use this reference for ordinary task creation or route configuration.

## Recommended Default

Prefer a user-level `systemd` service unless the user explicitly wants a system-wide service.

Repository assumptions:

- repo root: `/home/admin/code/tool/ai-sched-cli`
- config path: `~/.config/ai-sched-cli/config.json`
- command: `go run ./cmd/ai-sched-cli --config ~/.config/ai-sched-cli/config.json daemon`

## Minimal Persistent Start

Use this only when the user wants a quick background start, not formal boot integration:

```bash
cd /home/admin/code/tool/ai-sched-cli
go run ./cmd/ai-sched-cli daemon --ensure
go run ./cmd/ai-sched-cli daemon --status
```

## Recommended systemd Unit

```ini
[Unit]
Description=ai-sched-cli daemon
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=/home/admin/code/tool/ai-sched-cli
ExecStart=/usr/bin/env bash -lc 'go run ./cmd/ai-sched-cli --config ~/.config/ai-sched-cli/config.json daemon'
Restart=always
RestartSec=3

[Install]
WantedBy=default.target
```

## User-Level Setup Example

```bash
mkdir -p ~/.config/systemd/user
cat > ~/.config/systemd/user/ai-sched-cli.service <<'EOF'
[Unit]
Description=ai-sched-cli daemon
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=/home/admin/code/tool/ai-sched-cli
ExecStart=/usr/bin/env bash -lc 'go run ./cmd/ai-sched-cli --config ~/.config/ai-sched-cli/config.json daemon'
Restart=always
RestartSec=3

[Install]
WantedBy=default.target
EOF

systemctl --user daemon-reload
systemctl --user enable --now ai-sched-cli.service
systemctl --user status ai-sched-cli.service
```

## Verification

After setup, verify both the service and the scheduler itself:

```bash
cd /home/admin/code/tool/ai-sched-cli
go run ./cmd/ai-sched-cli status
go run ./cmd/ai-sched-cli daemon --status
```

If the user asks for channel-specific validation, also verify:

- WeChat bridge is running
- Dida CLI is logged in and reachable
- webhook or WeCom endpoint is configured

## Skill Guidance

When using this reference:

1. say explicitly that you are switching from normal scheduling to long-running deployment
2. preview the exact unit path and config path before writing files
3. prefer user-level `systemd` by default
4. after setup, verify service health and `ai-sched-cli` health
