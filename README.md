# ai-sched-cli

`ai-sched-cli` is a Linux-only local AI scheduler that stores natural-language tasks,
executes them at runtime with contextual AI evaluation, and routes results through
configured notification channels.

The current runtime model is:

- tasks store execution parameters up front
- the scheduler runs stored agent commands via `acpx`
- task tags can resolve into routed delivery channels before insert
- execution results are written to SQLite first
- notification delivery is handled asynchronously through an outbox loop

Current status:

- Local CLI foundation is implemented
- GitHub Actions builds Linux binaries for release-style use
- Config and SQLite bootstrap are working
- Task CRUD commands are available
- `daemon --ensure` provides idempotent background startup
- `daemon` polling and due-task execution are working
- `webhook`, `wecom_robot`, `dida`, and `wechat` delivery are implemented
- Runtime result delivery uses an outbox queue with retry
- One task can fan out to multiple delivery channels
- Tag-route configuration can map semantic tags like `work` or `urgent` to channel lists
- Local one-shot agent execution supports `codex`, `opencode`, and `claude`

WeChat support note:

- The current `wechat` channel is intentionally implemented against `wechat-bridge-opencode`
- This matches the author's current primary WeChat workflow in `/home/admin/code/re`
- Right now the scheduler sends to the bridge's last active WeChat user
- Other WeChat bridge implementations may be supported later, but are not part of the current compatibility target

Available commands:

- `ai-sched-cli init`
- `ai-sched-cli add`
- `ai-sched-cli list`
- `ai-sched-cli remove`
- `ai-sched-cli status`
- `ai-sched-cli tag-route`
- `ai-sched-cli daemon`
- `ai-sched-cli runs`
- `ai-sched-cli run-show`
- `ai-sched-cli version`

Planned layers:

1. Confirmation / clarification layer
2. Richer skill-side task orchestration
3. Additional bridge compatibility beyond the current WeChat path

Quick start:

```bash
go build -o ./bin/ai-sched-cli ./cmd/ai-sched-cli
./bin/ai-sched-cli init
./bin/ai-sched-cli tag-route set urgent --channel dida --channel wechat
./bin/ai-sched-cli tag-route set work --channel webhook
./bin/ai-sched-cli add --summary "check CI" --in 30m --channel wecom_robot --channel wechat
./bin/ai-sched-cli list
./bin/ai-sched-cli daemon --ensure
./bin/ai-sched-cli status
```

Skill-oriented runtime setup:

```bash
skills/schedule-task-manager/scripts/setup-runtime.sh
skills/schedule-task-manager/scripts/check-availability.sh
```

Direct GitHub install:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/zongqir/ai-scheduled-tasks/main/skills/schedule-task-manager/scripts/setup-runtime.sh) --non-interactive
```

More reliable GitHub bootstrap:

```bash
tmpdir="$(mktemp -d)"
git clone --depth 1 https://github.com/zongqir/ai-scheduled-tasks.git "$tmpdir/repo"
"$tmpdir/repo/skills/schedule-task-manager/scripts/setup-runtime.sh" --non-interactive
"$tmpdir/repo/skills/schedule-task-manager/scripts/check-availability.sh"
```

Archive-based fallback when `raw.githubusercontent.com` is flaky:

```bash
tmpdir="$(mktemp -d)"
curl -fsSL https://github.com/zongqir/ai-scheduled-tasks/archive/refs/heads/main.tar.gz \
  | tar -xz -C "$tmpdir"
"$tmpdir/ai-scheduled-tasks-main/skills/schedule-task-manager/scripts/setup-runtime.sh" --non-interactive
"$tmpdir/ai-scheduled-tasks-main/skills/schedule-task-manager/scripts/check-availability.sh"
```

Publishing a GitHub Release:

```bash
git tag v0.1.0
git push origin v0.1.0
```

Tag pushes matching `v*` publish a GitHub Release with:

- `ai-sched-cli-linux-amd64`
- `ai-sched-cli-linux-arm64`
- `ai-sched-cli-checksums.txt`

Notes:

- Current storage is SQLite via `modernc.org/sqlite`
- The module now targets Go `1.25`
- The default AI runtime is `acpx`
- On first-time interactive initialization, `init` asks which `acpx` agent should run scheduled AI tasks (for example `codex`, `opencode`, or `claude`). It does not silently choose one. For scripts and CI, pass it explicitly: `ai-sched-cli init --agent codex`.
- Existing configurations retain their configured `ai.agent`; use `ai-sched-cli init --agent <name>` to switch it without overwriting the rest of the config.
- For Agents and skills, prefer a compiled `ai-sched-cli` binary over `go run`
- `setup-runtime.sh` prefers the latest GitHub Release binary and falls back to clone+build when no release asset is available
- `raw.githubusercontent.com` can still time out occasionally; prefer the clone/archive bootstrap above when you want the most reliable GitHub-based setup path
- Release binaries are published by pushing a `v*` git tag, and tagged builds embed that tag in `ai-sched-cli version`
- Repeating `--channel` / `--channel-ref` builds a channel fan-out list for one task
- Tasks default to notifying, but `add` / `update` also support `--no-notify` for silent execution
- Channel selection priority is: explicit `--channel` > matched `tag_routes` > `default_channel`
- `ai-sched-cli tag-route set <tag> ...` is intended to be used by the higher-level scheduling skill
- `daemon --ensure` is intended to be safe to call after every task create/update from an external skill
- The current `wechat` channel depends on a running `wechat-bridge-opencode` bridge
- Scheduled time is an execution target, not a real-time delivery guarantee. The daemon detects due tasks on `daemon.poll_interval_seconds` (1 second by default), then queues notifications after task execution; the outbox is processed on `daemon.notification_poll_seconds` (10 seconds by default). AI runtime, channel, and network latency add further delay.
- Detailed design remains in [plan.md](./plan.md)
