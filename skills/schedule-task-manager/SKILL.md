---
name: schedule-task-manager
description: >-
  Use this skill when the user describes a planning, reminder, schedule, or task-management request in natural language,
  such as adding a timed task, deleting a scheduled reminder, changing an existing schedule,
  listing upcoming tasks, or inspecting past runs. The skill should interpret the user's intent,
  preview the planned action in plain language, ask a concise confirmation question when ambiguous,
  and then execute the local `ai-sched-cli` commands in this repository.
---

# Schedule Task Manager

Use this skill when the user speaks in natural language about:

- adding a reminder or scheduled task
- deleting a task
- changing the time, summary, channel, or cwd of a task
- adding or changing task tags
- configuring tag-to-channel routing rules
- configuring long-running deployment or startup behavior
- listing tasks
- inspecting runs or task details

This skill is the natural-language front end. The Go CLI in this repository is the execution back end.

Companion references in this skill folder:

- `LONG_RUNNING_REFERENCE.md`: use only when the user explicitly asks for long-running setup, startup on boot, or service deployment

## Core Rule

Do not ask the user to manually provide CLI flags unless there is real ambiguity.

Instead:

1. infer the intended operation
2. inspect existing tasks when needed
3. say what you are about to do
4. if the request is ambiguous, ask one short clarification question
5. otherwise execute the local command

## Repository Commands

Run commands from the repository root.

- Create task:
  - `go run ./cmd/ai-sched-cli add "<natural language>"`
  - or use explicit flags when the schedule is already resolved
  - tags: `--tag work --tag ci`
  - channels: repeat `--channel`, optional aligned `--channel-ref`
  - after a successful create, always run `go run ./cmd/ai-sched-cli daemon --ensure`
- List tasks:
  - `go run ./cmd/ai-sched-cli list`
  - `go run ./cmd/ai-sched-cli list --all`
- Show one task:
  - `go run ./cmd/ai-sched-cli show <task-id>`
- Manage tag routes:
  - `go run ./cmd/ai-sched-cli tag-route list`
  - `go run ./cmd/ai-sched-cli tag-route set <tag> --channel <name> [--channel-ref <ref>]`
  - `go run ./cmd/ai-sched-cli tag-route remove <tag>`
- Update one task:
  - `go run ./cmd/ai-sched-cli update <task-id> [flags]`
  - tags: `--tag work --tag ci` or `--clear-tags`
  - channels: repeat `--channel`, optional aligned `--channel-ref`, or `--clear-channels`
  - after a successful update, always run `go run ./cmd/ai-sched-cli daemon --ensure`
- Delete one task:
  - `go run ./cmd/ai-sched-cli remove <task-id>`
- Manually run one task:
  - `go run ./cmd/ai-sched-cli run <task-id>`
- Show run history:
  - `go run ./cmd/ai-sched-cli runs [task-id]`
- Show run details:
  - `go run ./cmd/ai-sched-cli run-show <run-id>`

## Intent Mapping

Map user requests into one of these operations:

- `create`
- `list`
- `show`
- `update`
- `delete`
- `run`
- `inspect-runs`
- `configure-tag-route`
- `configure-long-running`

Examples:

- "明天上午九点提醒我看 CI" -> `create`
- "删掉刚才那个任务" -> `delete`
- "把每天六点那个改成七点" -> `update`
- "给这个任务打上 work 和 ci 标签" -> `update`
- "把紧急标签路由到滴答和微信" -> `configure-tag-route`
- "帮我把这个常驻跑起来" -> `configure-long-running`
- "看看接下来有哪些任务" -> `list`
- "看看这个任务上次跑了什么" -> `inspect-runs`

## Directory Resolution Policy

The backend supports both explicit `--cwd` and implicit current-directory context.

Use this policy:

1. If the user gives an explicit path, resolve that path and pass `--cwd <dir>`
2. If the user names a repo or project rather than a full path:
   - infer the most likely local directory
   - preview the resolved directory in plain language
   - if there are multiple plausible directories, ask one clarification question
3. If the user does not mention a directory at all:
   - rely on the current working directory where the command is being issued
4. Do not ask the user for a manual path unless the directory is genuinely ambiguous

Good preview examples:

- "我将把任务添加到 `/home/admin/code/project-a` 下。"
- "我将修改 `/home/admin/code/moviepilot` 这个目录下的任务。"

## Tag Policy

Use tags when the user expresses category-like metadata such as:

- work
- ci
- urgent
- personal
- follow-up

If the user explicitly mentions labels/tags, pass them through CLI flags:

- create: `--tag work --tag ci`
- update: `--tag work --tag ci`
- clear tags: `--clear-tags`

If tags are only implied and not important to the request, do not invent them aggressively.

## Tag Route Policy

Treat tag routes as durable configuration, not one-off task parameters.

Examples:

- "紧急都发滴答和微信" -> set `urgent -> [dida, wechat]`
- "工作默认走 webhook" -> set `work -> [webhook]`
- "删掉 personal 的路由" -> remove `personal`

Execution rules:

1. Explicit task channels win over tag routes
2. If no explicit channels are given, the CLI resolves channels from matching tag routes
3. If no route matches, the CLI falls back to `default_channel`

## Preview Policy

Before mutating anything, say what you are about to do in plain language.

Good examples:

- "我将创建任务：明天上午 9 点提醒你看 CI。"
- "我将创建任务：明天上午 9 点在 `/home/admin/code/project-a` 下提醒你看 CI，标签为 `work,ci`。"
- "我将删除任务 `task_xxx`：每天下午 6 点看 CI。"
- "我将把任务 `task_xxx` 的执行时间从 18:00 改到 19:00。"

## Confirmation Policy

Ask a clarification question when:

- the target task is ambiguous
- the schedule interpretation is ambiguous
- multiple existing tasks plausibly match the request
- the user refers to relative phrases like "刚才那个" but no clear candidate exists

Prefer one concise question, not a survey.

Examples:

- "你说的‘刚才那个任务’，我是理解为 `task_xxx` 这条‘看 CI’任务，对吗？"
- "你说的‘明早’，我先按明天 09:00 理解，可以吗？"

## Safe Execution Strategy

For `create`:

- If the user gave natural language only, prefer `go run ./cmd/ai-sched-cli add "<request>"`
- Let the CLI's AI create-task path resolve the schedule
- If directory or tags are already confidently resolved, prefer explicit flags such as `--cwd` and `--tag`
- If the user names one or more delivery channels, pass them with repeated `--channel` flags
- If the user only expresses semantic intent like "工作" or "紧急", prefer tags and let the CLI resolve channels from configured tag routes
- After task creation succeeds, always call `go run ./cmd/ai-sched-cli daemon --ensure`

For `configure-tag-route`:

- Prefer `tag-route set` for create/update semantics
- Prefer `tag-route remove` when the user clearly wants to delete a route
- Use repeated `--channel` flags for fan-out routes
- Preview the final mapping before changing it

For `configure-long-running`:

- Only enter this path when the user explicitly asks for long-running behavior, startup on boot, or service deployment
- Use `LONG_RUNNING_REFERENCE.md` in this skill folder as the setup reference
- Prefer user-level `systemd` unless the user explicitly asks for a system-wide service
- After setup, verify both service status and `ai-sched-cli daemon --status`

For `update` and `delete`:

- Inspect current tasks first with `list --all` and `show <task-id>` when needed
- Resolve the exact target before executing
- For updates involving cwd or tags, prefer explicit `update <task-id> --cwd ... --tag ...`
- For channel changes, prefer explicit repeated `--channel` and aligned `--channel-ref`
- After a successful `update`, always call `go run ./cmd/ai-sched-cli daemon --ensure`

For `inspect-runs`:

- Use `runs <task-id>` to locate a run
- Use `run-show <run-id>` for full details

## Notes

- The backend stores execution parameters with each task and later runs them by assembling an `acpx` command at trigger time
- The backend already supports manual run inspection
- The daemon startup command is intentionally idempotent so the skill can call it every time after create/update
- Long-running deployment guidance lives in `LONG_RUNNING_REFERENCE.md` inside this skill folder
- This skill should keep the user interaction natural and concise instead of exposing backend flags by default
