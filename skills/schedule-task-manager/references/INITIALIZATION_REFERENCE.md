# Initialization Reference

Use this reference when the scheduler has no configuration yet, when the user
wants to change its AI agent or notification setup, or before declaring a new
runtime ready. Do not use it for ordinary task creation when `status` already
shows a healthy configuration.

## Goal

Initialization persists two user-facing defaults:

- `ai.agent`: the default agent for future `run_agent` tasks
- notification routing: the default delivery route for future notified tasks

Neither default is inferred from the AI currently running the Skill. In
particular, do not silently choose `codex` and do not assume WeChat on a
machine where it has not been configured.

## Determine Current State

Use the requested `--config` path. When none is given, use:

```text
~/.config/ai-sched-cli/config.json
```

If the file exists, run:

```bash
ai-sched-cli status --no-check
```

Report the displayed `AI runtime`, `AI agent`, `default channel`, and enabled
channels. Reuse the configured agent and route unless the user explicitly asks
to change them.

If the file does not exist, do not create AI tasks or start the daemon yet.
Complete the following initialization flow first.

## First-Time Flow

Ask one concise question before invoking `init`:

> 定时 AI 任务默认使用哪个 agent？例如 `codex`、`opencode` 或 `claude`。

After the user answers, preview the outcome and run:

```bash
ai-sched-cli init --agent <selected-agent>
```

The command writes `<selected-agent>` into `ai.agent`; this becomes the default
for future `run_agent` tasks. In an interactive terminal, `init` can ask for
the same value itself. AI-driven and non-interactive workflows must always pass
`--agent` explicitly so there is no silent default.

After `init`, run:

```bash
ai-sched-cli status
```

## Notification Setup

Inspect the post-init status before claiming notifications are ready.

- If `default channel: wechat`, say ordinary notified tasks will send WeChat
  reminders by default.
- If there is no default channel or no enabled channel, explain that notified
  tasks need an explicit `--channel`, a matching tag route, or `--no-notify`.
  Ask whether the user wants to configure a channel now.
- If a channel is enabled but incomplete, identify the missing value and ask
  whether to complete that channel now.

Channel requirements:

- `webhook`: `url`
- `wecom_robot`: `webhook_url`
- `dida`: `cli_path`, optional project and priority defaults
- `wechat`: a running bridge, plus optional `bridge_url` and `state_file`

Edit the config only after obtaining needed channel values, then rerun
`ai-sched-cli status` to verify it. Run `ai-sched-cli daemon --ensure` after a
successful configuration change when the user wants scheduled tasks to run.

## Change Existing Defaults

To change only the default agent, preview the new value and run:

```bash
ai-sched-cli init --agent <selected-agent>
```

This preserves channel configuration and other settings. Use `init --force`
only when the user explicitly requests a full configuration reset.

## Timing Expectation

Tell users of time-sensitive tasks that a scheduled time is an execution target,
not a precise message-delivery guarantee. The default daemon detects due tasks
about every second. A notification is queued only after task execution, and the
outbox is normally checked every 10 seconds. Agent execution, the WeChat bridge
or another channel, and network latency can add further delay.

## Required Confirmation Language

For a normal task using a configured WeChat default:

> 到点后由 `<agent>` 执行，完成后通过微信提醒你。

When the user explicitly says "本次不提醒" or "不用提醒", use `--no-notify`
without a follow-up question and confirm:

> 本次任务不会再提醒。
