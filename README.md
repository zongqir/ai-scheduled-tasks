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
ai-sched-cli init
ai-sched-cli tag-route set urgent --channel dida --channel wechat
ai-sched-cli tag-route set work --channel webhook
ai-sched-cli add --summary "check CI" --in 30m --channel wecom_robot --channel wechat
ai-sched-cli list
ai-sched-cli daemon --ensure
ai-sched-cli status
```

Notes:

- Current storage is SQLite via `modernc.org/sqlite`
- The module now targets Go `1.25`
- The default AI runtime is `acpx`
- Repeating `--channel` / `--channel-ref` builds a channel fan-out list for one task
- Channel selection priority is: explicit `--channel` > matched `tag_routes` > `default_channel`
- `ai-sched-cli tag-route set <tag> ...` is intended to be used by the higher-level scheduling skill
- `daemon --ensure` is intended to be safe to call after every task create/update from an external skill
- The current `wechat` channel depends on a running `wechat-bridge-opencode` bridge
- Detailed design remains in [plan.md](./plan.md)
