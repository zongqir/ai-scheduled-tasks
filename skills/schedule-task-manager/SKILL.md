---
name: schedule-task-manager
version: 1.0.0
description: >-
  Use this skill when the user wants to create, update, delete, inspect, or run
  scheduled AI tasks in natural language, configure tag-to-channel routing, or
  set up long-running scheduler deployment for this repository. Prefer the
  compiled `ai-sched-cli` binary over `go run`, use setup references when the
  runtime may be missing dependencies, preview the planned action in plain
  language, ask one concise clarification question when needed, and then
  execute the local scheduler commands.
---

# Schedule Task Manager

Use this skill when the user speaks in natural language about:

- adding a reminder or scheduled task
- deleting a task
- changing the time, summary, channel, or cwd of a task
- adding or changing task tags
- configuring tag-to-channel routing rules
- checking scheduler/runtime availability
- setting up the compiled scheduler runtime and dependencies
- configuring long-running deployment or startup behavior
- listing tasks
- inspecting runs or task details

This skill is the natural-language front end. The Go CLI in this repository is the execution back end.

## Reference Files

Read additional files in this skill directory only when the current user request
actually needs them.

- `scripts/check-availability.sh`
  Run this script when the user asks whether the scheduler setup is usable, or
  before long-running deployment when you need to verify required dependencies
  such as the configured AI command, `acpx`, `dida365`, or the WeChat bridge.
  If `raw.githubusercontent.com` is flaky, prefer a temporary Git clone or
  GitHub archive download over repeated raw-script fetches.
- `scripts/setup-runtime.sh`
  Run this script when the user wants a compiled binary installed locally, or
  when they want guided dependency setup with install-or-skip choices. The
  script prefers the latest GitHub Release binary and falls back to source
  build when needed, so it can also be used from a standalone GitHub-downloaded
  copy. When reliability matters more than one-line brevity, prefer cloning or
  unpacking the GitHub repository and executing the script from that temporary
  checkout instead of depending on raw-script fetch availability.
- `references/LONG_RUNNING_REFERENCE.md`
  Use this reference only when the user explicitly asks for long-running setup,
  startup on boot, background service management, or stable deployment.

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

Assume the compiled binary name is `ai-sched-cli`. If the binary is missing,
first use `setup-runtime.sh` instead of defaulting to `go run`.

- Create task:
  - `ai-sched-cli add "<natural language>"`
  - or use explicit flags when the schedule is already resolved
  - tags: `--tag work --tag ci`
  - channels: repeat `--channel`, optional aligned `--channel-ref`
  - after a successful create, always run `ai-sched-cli daemon --ensure`
- List tasks:
  - `ai-sched-cli list`
  - `ai-sched-cli list --all`
- Show one task:
  - `ai-sched-cli show <task-id>`
- Manage tag routes:
  - `ai-sched-cli tag-route list`
  - `ai-sched-cli tag-route set <tag> --channel <name> [--channel-ref <ref>]`
  - `ai-sched-cli tag-route remove <tag>`
- Update one task:
  - `ai-sched-cli update <task-id> [flags]`
  - tags: `--tag work --tag ci` or `--clear-tags`
  - channels: repeat `--channel`, optional aligned `--channel-ref`, or `--clear-channels`
  - after a successful update, always run `ai-sched-cli daemon --ensure`
- Delete one task:
  - `ai-sched-cli remove <task-id>`
- Manually run one task:
  - `ai-sched-cli run <task-id>`
- Show run history:
  - `ai-sched-cli runs [task-id]`
- Show run details:
  - `ai-sched-cli run-show <run-id>`

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
- `configure-channel`
- `check-availability`
- `setup-runtime`
- `configure-long-running`

Examples:

- "明天上午九点提醒我看 CI" -> `create`
- "删掉刚才那个任务" -> `delete`
- "把每天六点那个改成七点" -> `update`
- "给这个任务打上 work 和 ci 标签" -> `update`
- "把紧急标签路由到滴答和微信" -> `configure-tag-route`
- "配置一下通知渠道" -> `configure-channel`
- "看看现在这套能不能跑" -> `check-availability`
- "先帮我把这个装好，缺什么你自己处理，不能装的就先跳过" -> `setup-runtime`
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

- If the user gave natural language only, prefer `ai-sched-cli add "<request>"`
- Let the CLI's AI create-task path resolve the schedule
- If directory or tags are already confidently resolved, prefer explicit flags such as `--cwd` and `--tag`
- If the user names one or more delivery channels, pass them with repeated `--channel` flags
- If the user only expresses semantic intent like "工作" or "紧急", prefer tags and let the CLI resolve channels from configured tag routes
- After task creation succeeds, always call `ai-sched-cli daemon --ensure`

For `configure-tag-route`:

- Prefer `tag-route set` for create/update semantics
- Prefer `tag-route remove` when the user clearly wants to delete a route
- Use repeated `--channel` flags for fan-out routes
- Preview the final mapping before changing it

For `configure-channel`:

- When the user wants to set up a notification channel, first check current config with `ai-sched-cli status`
- List available channels: webhook, wecom_robot, dida, wechat
- Ask the user which channel to configure and collect the required values:
  - webhook: url
  - wecom_robot: webhook_url
  - dida: cli_path (default: dida365), default_project_id, default_priority
  - wechat: bridge_url, state_file
- Edit the config file at the resolved config path (shown by `ai-sched-cli status`) to set the values
- After editing, run `ai-sched-cli status` to verify the channel is now properly configured
- Always run `ai-sched-cli daemon --ensure` after changing config

For `check-availability`:

- Run `scripts/check-availability.sh` from this skill directory
- Pass `--config <path>` when the user or environment uses a non-default config
- Treat missing configured AI command as a hard failure
- Treat disabled optional channels as skipped, not failed
- Treat enabled-but-unconfigured channels (e.g., webhook with empty url) as WARN, not FAIL — the user can ignore or fix them later
- When the script reports failures, summarize the exact missing dependency and the shortest fix path

For `setup-runtime`:

- Run `scripts/setup-runtime.sh` from this skill directory
- Prefer installing the compiled `ai-sched-cli` binary into `~/.local/bin`
- Let the script offer install-or-skip choices for missing dependencies
- At minimum, ensure the configured AI command or `acpx` is addressed before claiming the setup is usable
- After binary install, run `ai-sched-cli init` if no config exists yet
- After init, always check channel configuration:
  1. Run `ai-sched-cli status`
  2. If `default channel: -` or `enabled channels: -`, tell the user notifications are not configured yet and ask whether to configure a channel now
     - If yes, use `configure-channel`
     - If no, remind them that future notified tasks must use an explicit `--channel`, matching tag routes, or `--no-notify`
  3. If any channels are enabled but not configured (e.g., webhook URL empty), ask the user:
     - "xx 渠道已启用但未配置，是否现在配置？[Y/n]"
     - If yes, guide the user to fill in the required fields (edit the config JSON or use the appropriate setup)
     - If no, leave it as-is (the user knowingly accepts the unconfigured state)
  4. After handling, run `ai-sched-cli status` again to confirm
- After setup, run `check-availability.sh` or `ai-sched-cli status` to verify the result

For `configure-long-running`:

- Only enter this path when the user explicitly asks for long-running behavior, startup on boot, or service deployment
- Use `references/LONG_RUNNING_REFERENCE.md` in this skill folder as the setup reference
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
- Long-running deployment guidance lives in `references/LONG_RUNNING_REFERENCE.md` inside this skill folder
- This skill should keep the user interaction natural and concise instead of exposing backend flags by default
