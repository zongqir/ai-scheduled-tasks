package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"ai-sched-cli/internal/ai"
	"ai-sched-cli/internal/config"
	"ai-sched-cli/internal/db"
	"ai-sched-cli/internal/executor"
	"ai-sched-cli/internal/logx"
	"ai-sched-cli/internal/scheduler"
	"ai-sched-cli/internal/task"
)

const version = "dev"

func Run(args []string) error {
	globalConfigPath, trimmedArgs, err := extractGlobalConfigPath(args)
	if err != nil {
		return err
	}

	if len(trimmedArgs) == 0 {
		printUsage()
		return nil
	}

	switch trimmedArgs[0] {
	case "help":
		printUsage()
		return nil
	case "version":
		fmt.Printf("ai-sched-cli %s\n", version)
		return nil
	case "init":
		return runInit(globalConfigPath, trimmedArgs[1:])
	case "add":
		return runAdd(globalConfigPath, trimmedArgs[1:])
	case "list":
		return runList(globalConfigPath, trimmedArgs[1:])
	case "show":
		return runShowTask(globalConfigPath, trimmedArgs[1:])
	case "runs":
		return runRuns(globalConfigPath, trimmedArgs[1:])
	case "run-show":
		return runShow(globalConfigPath, trimmedArgs[1:])
	case "remove":
		return runRemove(globalConfigPath, trimmedArgs[1:])
	case "run":
		return runTask(globalConfigPath, trimmedArgs[1:])
	case "update":
		return runUpdate(globalConfigPath, trimmedArgs[1:])
	case "status":
		return runStatus(globalConfigPath, trimmedArgs[1:])
	case "tag-route":
		return runTagRoute(globalConfigPath, trimmedArgs[1:])
	case "daemon":
		return runDaemon(globalConfigPath, trimmedArgs[1:])
	default:
		return fmt.Errorf("unknown command: %s", trimmedArgs[0])
	}
}

func runInit(globalConfigPath string, args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	force := fs.Bool("force", false, "overwrite config with defaults")
	if err := fs.Parse(args); err != nil {
		return err
	}

	configPath, err := resolveConfigPath(globalConfigPath)
	if err != nil {
		return err
	}

	var (
		cfg     config.Config
		created bool
	)

	switch {
	case *force:
		cfg = config.Default()
		if err := config.Save(configPath, cfg); err != nil {
			return err
		}
		created = true
	default:
		var ensureErr error
		cfg, created, ensureErr = config.Ensure(configPath)
		if ensureErr != nil {
			return ensureErr
		}
	}

	appDB, err := openDBFromConfig(cfg)
	if err != nil {
		return err
	}
	defer appDB.Close()

	if err := appDB.Ping(context.Background()); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}

	resolvedConfigPath, err := config.ExpandPath(configPath)
	if err != nil {
		return err
	}
	resolvedDBPath, err := cfg.ResolvedDatabasePath()
	if err != nil {
		return err
	}

	if created {
		fmt.Printf("initialized config: %s\n", resolvedConfigPath)
	} else {
		fmt.Printf("config already present: %s\n", resolvedConfigPath)
	}
	fmt.Printf("database ready: %s\n", resolvedDBPath)
	fmt.Printf("default channel: %s\n", cfg.DefaultChannel)
	fmt.Printf("enabled channels: %s\n", strings.Join(cfg.EnabledChannels(), ", "))
	return nil
}

func runAdd(globalConfigPath string, args []string) error {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	summary := fs.String("summary", "", "task summary")
	action := fs.String("action", "run_agent", "task action: run_agent or notify")
	agent := fs.String("agent", "", "agent name, e.g. codex, claude, or opencode")
	instruction := fs.String("instruction", "", "stored execution instruction or notification body")
	model := fs.String("model", "", "agent model override")
	at := fs.String("at", "", "absolute time, e.g. 2026-07-12 09:00 or RFC3339")
	in := fs.String("in", "", "relative duration, e.g. 30m, 2h")
	cwdFlag := fs.String("cwd", "", "execution working directory")
	repeatRule := fs.String("repeat", "", "repeat rule, e.g. daily or weekly|mon")
	timeOfDay := fs.String("time-of-day", "", "repeat time of day, format HH:MM[:SS]")
	notify := fs.Bool("notify", false, "explicitly notify when the task runs")
	noNotify := fs.Bool("no-notify", false, "run the task without sending notifications")
	var channels multiStringFlag
	var channelRefs multiStringFlag
	fs.Var(&channels, "channel", "channel override, repeatable or comma-separated")
	fs.Var(&channelRefs, "channel-ref", "channel-specific reference, aligned by position with --channel")
	var tags multiStringFlag
	fs.Var(&tags, "tag", "task tag, repeatable or comma-separated")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, repo, cleanup, err := openRepository(globalConfigPath)
	if err != nil {
		return err
	}
	defer cleanup()

	rawInput := strings.TrimSpace(strings.Join(fs.Args(), " "))
	taskSummary := strings.TrimSpace(*summary)
	if taskSummary == "" {
		taskSummary = rawInput
	}
	if taskSummary == "" {
		return fmt.Errorf("summary is required; pass --summary or positional text")
	}

	cwd, err := resolveTaskCWD(*cwdFlag)
	if err != nil {
		return err
	}

	notifyPolicy, err := resolveNotifyPolicy(*notify, *noNotify)
	if err != nil {
		return err
	}
	taskChannels, err := resolveCreateChannels(cfg, channels.Values(), channelRefs.Values(), tags.Values(), notifyPolicy != task.NotifyPolicyOff)
	if err != nil {
		return err
	}

	hasExplicitSchedule := strings.TrimSpace(*at) != "" || strings.TrimSpace(*in) != "" || strings.TrimSpace(*repeatRule) != ""
	if !hasExplicitSchedule {
		return runAddWithAI(cfg, repo, coalesceRawInput(rawInput, taskSummary), taskSummary, cwd, taskChannels, tags.Values(), notifyPolicy)
	}

	taskAction := task.Action(strings.TrimSpace(*action))
	taskAgent := strings.TrimSpace(*agent)
	if taskAgent == "" {
		taskAgent = cfg.AI.Agent
	}
	taskInstruction := strings.TrimSpace(*instruction)
	if taskInstruction == "" {
		taskInstruction = coalesceRawInput(rawInput, taskSummary)
	}

	nextRunAt, runAt, scheduleType, derivedTimeOfDay, err := resolveSchedule(cfg.Timezone, *at, *in, *repeatRule)
	if err != nil {
		return err
	}
	if strings.TrimSpace(*timeOfDay) != "" {
		derivedTimeOfDay = strings.TrimSpace(*timeOfDay)
	}

	record, err := repo.Create(context.Background(), task.CreateInput{
		RawInput:      rawInput,
		Summary:       taskSummary,
		Action:        taskAction,
		Agent:         taskAgent,
		Instruction:   taskInstruction,
		Model:         strings.TrimSpace(*model),
		ScheduleType:  scheduleType,
		Timezone:      cfg.Timezone,
		RunAt:         runAt,
		RepeatRule:    strings.TrimSpace(*repeatRule),
		TimeOfDay:     derivedTimeOfDay,
		NextRunAt:     nextRunAt,
		CWD:           cwd,
		NotifyPolicy:  notifyPolicy,
		Channel:       firstChannelName(taskChannels),
		ChannelRef:    firstChannelRef(taskChannels),
		Channels:      taskChannels,
		Tags:          tags.Values(),
		ConfirmStatus: task.ConfirmNone,
	})
	if err != nil {
		return err
	}

	fmt.Printf("created task %s\n", record.ID)
	fmt.Printf("summary: %s\n", record.Summary)
	fmt.Printf("action: %s\n", record.Action)
	fmt.Printf("agent: %s\n", emptyDash(record.Agent))
	fmt.Printf("model: %s\n", emptyDash(record.Model))
	fmt.Printf("instruction: %s\n", emptyDash(record.Instruction))
	fmt.Printf("next run: %s\n", formatUnix(record.NextRunAt, record.Timezone))
	fmt.Printf("notify policy: %s\n", record.NotifyPolicy)
	fmt.Printf("channels: %s\n", formatChannelTargets(record.EffectiveChannels()))
	fmt.Printf("cwd: %s\n", record.CWD)
	fmt.Printf("tags: %s\n", formatTags(record.Tags))
	return nil
}

func runAddWithAI(cfg config.Config, repo *task.Repository, rawInput, taskSummary, cwd string, taskChannels []task.ChannelTarget, tags []string, notifyPolicy task.NotifyPolicy) error {
	client := ai.Client{
		Command:    cfg.AI.Command,
		Args:       append([]string(nil), cfg.AI.Args...),
		Agent:      cfg.AI.Agent,
		Model:      cfg.AI.Model,
		Timeout:    time.Duration(cfg.AI.TimeoutSeconds) * time.Second,
		MaxRetries: cfg.AI.MaxRetries,
	}

	resp, _, _, err := client.CreateTask(context.Background(), ai.CreateTaskInput{
		RawInput:            rawInput,
		Timezone:            cfg.Timezone,
		CWD:                 cwd,
		DefaultChannel:      firstChannelName(taskChannels),
		DefaultNotifyPolicy: string(notifyPolicy),
		DefaultAgent:        cfg.AI.Agent,
	})
	if err != nil {
		return fmt.Errorf("ai create task: %w", err)
	}

	if resp.RequiresConfirmation {
		fmt.Println("confirmation required")
		fmt.Printf("question: %s\n", resp.Question)
		fmt.Printf("summary: %s\n", resp.DraftTask.Summary)
		fmt.Printf("proposed next run: %s\n", resp.DraftTask.NextRunAt)
		return nil
	}

	record, err := createTaskFromDraft(cfg, repo, rawInput, resp.Task, tags)
	if err != nil {
		return err
	}

	fmt.Printf("created task %s\n", record.ID)
	fmt.Printf("summary: %s\n", record.Summary)
	fmt.Printf("action: %s\n", record.Action)
	fmt.Printf("agent: %s\n", emptyDash(record.Agent))
	fmt.Printf("model: %s\n", emptyDash(record.Model))
	fmt.Printf("instruction: %s\n", emptyDash(record.Instruction))
	fmt.Printf("next run: %s\n", formatUnix(record.NextRunAt, record.Timezone))
	fmt.Printf("notify policy: %s\n", record.NotifyPolicy)
	fmt.Printf("channels: %s\n", formatChannelTargets(record.EffectiveChannels()))
	fmt.Printf("cwd: %s\n", record.CWD)
	fmt.Printf("tags: %s\n", formatTags(record.Tags))
	return nil
}

func runList(globalConfigPath string, args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	all := fs.Bool("all", false, "show all tasks")
	statusArg := fs.String("status", "", "comma-separated statuses")
	limit := fs.Int("limit", 50, "maximum rows to show")
	if err := fs.Parse(args); err != nil {
		return err
	}

	_, repo, cleanup, err := openRepository(globalConfigPath)
	if err != nil {
		return err
	}
	defer cleanup()

	filter := task.ListFilter{Limit: *limit}
	if *all {
		filter.Status = nil
	} else if strings.TrimSpace(*statusArg) != "" {
		filter.Status, err = parseStatuses(*statusArg)
		if err != nil {
			return err
		}
	} else {
		filter.Status = []task.Status{task.StatusPending, task.StatusRunning, task.StatusFailed}
	}

	records, err := repo.List(context.Background(), filter)
	if err != nil {
		return err
	}

	if len(records) == 0 {
		fmt.Println("no tasks found")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 2, 2, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tNEXT RUN\tNOTIFY\tCHANNELS\tTAGS\tSUMMARY")
	for _, record := range records {
		fmt.Fprintf(
			w,
			"%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			record.ID,
			record.Status,
			formatUnix(record.NextRunAt, record.Timezone),
			record.NotifyPolicy,
			formatChannelTargets(record.EffectiveChannels()),
			formatTags(record.Tags),
			record.Summary,
		)
	}
	return w.Flush()
}

func runRemove(globalConfigPath string, args []string) error {
	fs := flag.NewFlagSet("remove", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: ai-sched-cli remove <task-id>")
	}

	_, repo, cleanup, err := openRepository(globalConfigPath)
	if err != nil {
		return err
	}
	defer cleanup()

	ok, err := repo.Delete(context.Background(), fs.Arg(0))
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("task not found: %s", fs.Arg(0))
	}

	fmt.Printf("removed task %s\n", fs.Arg(0))
	return nil
}

func runTask(globalConfigPath string, args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: ai-sched-cli run <task-id>")
	}

	cfg, repo, cleanup, err := openRepository(globalConfigPath)
	if err != nil {
		return err
	}
	defer cleanup()

	logger := log.New(os.Stdout, logx.Name+" run: ", log.LstdFlags)
	runner := scheduler.Runner{
		Repo:    repo,
		Factory: channelFactory{config: cfg},
		Executor: executor.ACPXRunner{
			Command: cfg.AI.Command,
			Args:    append([]string(nil), cfg.AI.Args...),
		},
		PollInterval:        time.Duration(cfg.Daemon.PollIntervalSeconds) * time.Second,
		NotificationPoll:    time.Duration(cfg.Daemon.NotificationPollSeconds) * time.Second,
		StuckRunningTimeout: time.Duration(cfg.Daemon.StuckRunningTimeoutSeconds) * time.Second,
		AgentTimeout:        15 * time.Minute,
		NotificationRetries: cfg.Daemon.NotificationMaxRetries,
		Logger:              logger,
	}

	logger.Printf("running task %s", fs.Arg(0))
	return runner.RunTaskByID(context.Background(), fs.Arg(0))
}

func runRuns(globalConfigPath string, args []string) error {
	fs := flag.NewFlagSet("runs", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	limit := fs.Int("limit", 20, "maximum rows to show")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return fmt.Errorf("usage: ai-sched-cli runs [task-id] [--limit 20]")
	}

	_, repo, cleanup, err := openRepository(globalConfigPath)
	if err != nil {
		return err
	}
	defer cleanup()

	filter := task.RunListFilter{Limit: *limit}
	if fs.NArg() == 1 {
		filter.TaskID = fs.Arg(0)
	}

	records, err := repo.ListRuns(context.Background(), filter)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		fmt.Println("no runs found")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 2, 2, 2, ' ', 0)
	fmt.Fprintln(w, "RUN ID\tTASK ID\tSTATUS\tSTARTED\tFINISHED\tERROR")
	for _, record := range records {
		fmt.Fprintf(
			w,
			"%s\t%s\t%s\t%s\t%s\t%s\n",
			record.ID,
			record.TaskID,
			record.Status,
			formatUnix(record.StartedAt, time.Local.String()),
			formatMaybeUnix(record.FinishedAt, time.Local.String()),
			truncateText(record.Error, 80),
		)
	}
	return w.Flush()
}

func runShowTask(globalConfigPath string, args []string) error {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: ai-sched-cli show <task-id>")
	}

	cfg, repo, cleanup, err := openRepository(globalConfigPath)
	if err != nil {
		return err
	}
	defer cleanup()

	record, err := repo.GetByID(context.Background(), fs.Arg(0))
	if err != nil {
		return err
	}

	fmt.Printf("task: %s\n", record.ID)
	fmt.Printf("summary: %s\n", record.Summary)
	fmt.Printf("raw input: %s\n", emptyDash(record.RawInput))
	fmt.Printf("action: %s\n", record.Action)
	fmt.Printf("agent: %s\n", emptyDash(record.Agent))
	fmt.Printf("model: %s\n", emptyDash(record.Model))
	fmt.Printf("instruction: %s\n", emptyDash(record.Instruction))
	fmt.Printf("status: %s\n", record.Status)
	fmt.Printf("schedule: %s\n", record.ScheduleType)
	fmt.Printf("next run: %s\n", formatUnix(record.NextRunAt, record.Timezone))
	if record.RunAt > 0 {
		fmt.Printf("run at: %s\n", formatUnix(record.RunAt, record.Timezone))
	}
	fmt.Printf("repeat rule: %s\n", emptyDash(record.RepeatRule))
	fmt.Printf("time of day: %s\n", emptyDash(record.TimeOfDay))
	fmt.Printf("timezone: %s\n", record.Timezone)
	fmt.Printf("notify policy: %s\n", record.NotifyPolicy)
	fmt.Printf("channels: %s\n", formatChannelTargets(record.EffectiveChannels()))
	fmt.Printf("tags: %s\n", formatTags(record.Tags))
	fmt.Printf("tag-route channels: %s\n", formatConfigRouteTargets(cfg.ResolveTagRouteTargets(record.Tags)))
	fmt.Printf("cwd: %s\n", record.CWD)
	fmt.Printf("last error: %s\n", emptyDash(record.LastError))
	return nil
}

func runShow(globalConfigPath string, args []string) error {
	fs := flag.NewFlagSet("run-show", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: ai-sched-cli run-show <run-id>")
	}

	_, repo, cleanup, err := openRepository(globalConfigPath)
	if err != nil {
		return err
	}
	defer cleanup()

	record, err := repo.GetRunByID(context.Background(), fs.Arg(0))
	if err != nil {
		return err
	}

	fmt.Printf("run: %s\n", record.ID)
	fmt.Printf("task: %s\n", record.TaskID)
	fmt.Printf("status: %s\n", record.Status)
	fmt.Printf("started: %s\n", formatMaybeUnix(record.StartedAt, time.Local.String()))
	fmt.Printf("finished: %s\n", formatMaybeUnix(record.FinishedAt, time.Local.String()))
	fmt.Printf("error: %s\n", emptyDash(record.Error))
	fmt.Println("")
	fmt.Println("Stored plan:")
	fmt.Println(indentBlock(emptyDash(record.AIInput)))
	fmt.Println("")
	fmt.Println("Preparation output:")
	fmt.Println(indentBlock(emptyDash(record.AIOutput)))
	fmt.Println("")
	fmt.Println("Execution output:")
	fmt.Println(indentBlock(emptyDash(record.ExecOutput)))
	fmt.Println("")
	fmt.Println("Notify output:")
	fmt.Println(indentBlock(emptyDash(record.NotifyOutput)))
	return nil
}

func runUpdate(globalConfigPath string, args []string) error {
	taskID, remainingArgs, err := extractLeadingTaskID(args)
	if err != nil {
		return err
	}

	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	summary := fs.String("summary", "", "updated task summary")
	action := fs.String("action", "", "updated task action: run_agent or notify")
	agent := fs.String("agent", "", "updated agent name")
	instruction := fs.String("instruction", "", "updated execution instruction or notification body")
	model := fs.String("model", "", "updated model override")
	at := fs.String("at", "", "updated absolute time")
	in := fs.String("in", "", "updated relative duration")
	cwdFlag := fs.String("cwd", "", "updated cwd")
	repeatRule := fs.String("repeat", "", "updated repeat rule")
	timeOfDay := fs.String("time-of-day", "", "updated repeat time of day")
	notify := fs.Bool("notify", false, "explicitly notify when the task runs")
	noNotify := fs.Bool("no-notify", false, "run the task without sending notifications")
	var channels multiStringFlag
	var channelRefs multiStringFlag
	fs.Var(&channels, "channel", "updated channel, repeatable or comma-separated")
	fs.Var(&channelRefs, "channel-ref", "updated channel-specific reference, aligned by position with --channel")
	var tags multiStringFlag
	fs.Var(&tags, "tag", "updated task tag, repeatable or comma-separated")
	clearTags := fs.Bool("clear-tags", false, "clear all task tags")
	clearChannels := fs.Bool("clear-channels", false, "replace task channels with the config default channel")
	if err := fs.Parse(remainingArgs); err != nil {
		return err
	}
	if taskID == "" {
		if fs.NArg() == 1 {
			taskID = fs.Arg(0)
		} else {
			return fmt.Errorf("usage: ai-sched-cli update <task-id> [flags]")
		}
	}
	if fs.NArg() > 1 {
		return fmt.Errorf("usage: ai-sched-cli update <task-id> [flags]")
	}

	cfg, repo, cleanup, err := openRepository(globalConfigPath)
	if err != nil {
		return err
	}
	defer cleanup()

	existing, err := repo.GetByID(context.Background(), taskID)
	if err != nil {
		return err
	}
	if existing.Status == task.StatusRunning {
		return fmt.Errorf("cannot update a running task: %s", existing.ID)
	}

	updated := existing
	if strings.TrimSpace(*summary) != "" {
		updated.Summary = strings.TrimSpace(*summary)
	}
	if strings.TrimSpace(*action) != "" {
		updated.Action = task.Action(strings.TrimSpace(*action))
	}
	if strings.TrimSpace(*agent) != "" {
		updated.Agent = strings.TrimSpace(*agent)
	}
	if strings.TrimSpace(*instruction) != "" {
		updated.Instruction = strings.TrimSpace(*instruction)
	}
	if strings.TrimSpace(*model) != "" {
		updated.Model = strings.TrimSpace(*model)
	}
	if strings.TrimSpace(*cwdFlag) != "" {
		updated.CWD, err = resolveTaskCWD(*cwdFlag)
		if err != nil {
			return err
		}
	}
	if *notify || *noNotify {
		updated.NotifyPolicy, err = resolveNotifyPolicy(*notify, *noNotify)
		if err != nil {
			return err
		}
	}
	if updated.NotifyPolicy == task.NotifyPolicyOff {
		updated.Channels = nil
		updated.Channel = ""
		updated.ChannelRef = ""
	} else if *clearChannels {
		updated.Channels, err = resolveCreateChannels(cfg, nil, nil, updated.Tags, true)
		if err != nil {
			return err
		}
		updated.Channel = firstChannelName(updated.Channels)
		updated.ChannelRef = firstChannelRef(updated.Channels)
	} else if len(channels.Values()) > 0 || len(channelRefs.Values()) > 0 {
		updated.Channels, err = resolveTaskChannels(channels.Values(), channelRefs.Values(), "")
		if err != nil {
			return err
		}
		updated.Channel = firstChannelName(updated.Channels)
		updated.ChannelRef = firstChannelRef(updated.Channels)
	}
	if *clearTags {
		updated.Tags = nil
	} else if len(tags.Values()) > 0 {
		updated.Tags = tags.Values()
	}
	if updated.NotifyPolicy != task.NotifyPolicyOff && len(updated.EffectiveChannels()) == 0 {
		updated.Channels, err = resolveCreateChannels(cfg, nil, nil, updated.Tags, true)
		if err != nil {
			return err
		}
		updated.Channel = firstChannelName(updated.Channels)
		updated.ChannelRef = firstChannelRef(updated.Channels)
	}

	hasScheduleOverride := strings.TrimSpace(*at) != "" || strings.TrimSpace(*in) != "" || strings.TrimSpace(*repeatRule) != "" || strings.TrimSpace(*timeOfDay) != ""
	if hasScheduleOverride {
		updated, err = applyTaskScheduleUpdate(updated, *at, *in, *repeatRule, *timeOfDay)
		if err != nil {
			return err
		}
	}

	record, err := repo.Update(context.Background(), task.UpdateInput{
		ID:            updated.ID,
		RawInput:      updated.RawInput,
		Summary:       updated.Summary,
		Action:        updated.Action,
		Agent:         updated.Agent,
		Instruction:   updated.Instruction,
		Model:         updated.Model,
		ScheduleType:  updated.ScheduleType,
		Timezone:      updated.Timezone,
		RunAt:         updated.RunAt,
		RepeatRule:    updated.RepeatRule,
		TimeOfDay:     updated.TimeOfDay,
		NextRunAt:     updated.NextRunAt,
		CWD:           updated.CWD,
		NotifyPolicy:  updated.NotifyPolicy,
		Channel:       updated.Channel,
		ChannelRef:    updated.ChannelRef,
		Channels:      updated.Channels,
		Tags:          updated.Tags,
		ConfirmStatus: task.ConfirmNone,
		Status:        task.StatusPending,
	})
	if err != nil {
		return err
	}

	fmt.Printf("updated task %s\n", record.ID)
	fmt.Printf("summary: %s\n", record.Summary)
	fmt.Printf("action: %s\n", record.Action)
	fmt.Printf("agent: %s\n", emptyDash(record.Agent))
	fmt.Printf("model: %s\n", emptyDash(record.Model))
	fmt.Printf("instruction: %s\n", emptyDash(record.Instruction))
	fmt.Printf("next run: %s\n", formatUnix(record.NextRunAt, record.Timezone))
	fmt.Printf("notify policy: %s\n", record.NotifyPolicy)
	fmt.Printf("channels: %s\n", formatChannelTargets(record.EffectiveChannels()))
	fmt.Printf("cwd: %s\n", record.CWD)
	fmt.Printf("tags: %s\n", formatTags(record.Tags))
	return nil
}

func runStatus(globalConfigPath string, args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	noCheck := fs.Bool("no-check", false, "skip active health checks")
	if err := fs.Parse(args); err != nil {
		return err
	}

	configPath, err := resolveConfigPath(globalConfigPath)
	if err != nil {
		return err
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("config not initialized; run: ai-sched-cli init")
		}
		return err
	}

	appDB, err := openDBFromConfig(cfg)
	if err != nil {
		return err
	}
	defer appDB.Close()

	repo := task.NewRepository(appDB.SQL)
	stats, err := repo.Stats(context.Background())
	if err != nil {
		return err
	}

	resolvedConfigPath, err := config.ExpandPath(configPath)
	if err != nil {
		return err
	}
	resolvedDBPath, err := cfg.ResolvedDatabasePath()
	if err != nil {
		return err
	}

	fmt.Printf("config: %s\n", resolvedConfigPath)
	fmt.Printf("database: %s\n", resolvedDBPath)
	fmt.Printf("timezone: %s\n", cfg.Timezone)
	fmt.Printf("default channel: %s\n", cfg.DefaultChannel)
	fmt.Printf("enabled channels: %s\n", strings.Join(cfg.EnabledChannels(), ", "))
	fmt.Printf("tag routes: %d\n", len(cfg.TagRoutes))
	fmt.Printf("task counts: total=%d pending=%d running=%d done=%d failed=%d cancelled=%d\n",
		stats.Total, stats.Pending, stats.Running, stats.Done, stats.Failed, stats.Cancelled)
	if *noCheck {
		fmt.Println("health checks: skipped")
		return nil
	}

	fmt.Println("health checks:")
	for _, check := range collectHealthChecks(context.Background(), cfg) {
		fmt.Printf("  %-20s %-8s %s\n", check.Name, check.Status, check.Detail)
	}
	return nil
}

func runTagRoute(globalConfigPath string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: ai-sched-cli tag-route <list|set|remove> [...]")
	}

	switch args[0] {
	case "list":
		return runTagRouteList(globalConfigPath, args[1:])
	case "set":
		return runTagRouteSet(globalConfigPath, args[1:])
	case "remove":
		return runTagRouteRemove(globalConfigPath, args[1:])
	default:
		return fmt.Errorf("unknown tag-route command: %s", args[0])
	}
}

func runTagRouteList(globalConfigPath string, args []string) error {
	fs := flag.NewFlagSet("tag-route list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("usage: ai-sched-cli tag-route list")
	}

	configPath, err := resolveConfigPath(globalConfigPath)
	if err != nil {
		return err
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	if len(cfg.TagRoutes) == 0 {
		fmt.Println("no tag routes configured")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 2, 2, 2, ' ', 0)
	fmt.Fprintln(w, "TAG\tCHANNELS")
	for _, tag := range cfg.SortedTagRouteKeys() {
		fmt.Fprintf(w, "%s\t%s\n", tag, formatConfigRouteTargets(cfg.TagRoutes[tag]))
	}
	return w.Flush()
}

func runTagRouteSet(globalConfigPath string, args []string) error {
	tagName, remainingArgs, err := extractLeadingTaskID(args)
	if err != nil {
		return err
	}

	fs := flag.NewFlagSet("tag-route set", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var channels multiStringFlag
	var channelRefs multiStringFlag
	fs.Var(&channels, "channel", "route channel, repeatable or comma-separated")
	fs.Var(&channelRefs, "channel-ref", "route channel reference, aligned by position with --channel")
	if err := fs.Parse(remainingArgs); err != nil {
		return err
	}
	if tagName == "" {
		if fs.NArg() == 1 {
			tagName = fs.Arg(0)
		} else {
			return fmt.Errorf("usage: ai-sched-cli tag-route set <tag> --channel <name> [--channel-ref <ref>]")
		}
	}
	if fs.NArg() > 1 {
		return fmt.Errorf("usage: ai-sched-cli tag-route set <tag> --channel <name> [--channel-ref <ref>]")
	}

	targets, err := resolveConfigRouteTargets(channels.Values(), channelRefs.Values(), "")
	if err != nil {
		return err
	}

	configPath, err := resolveConfigPath(globalConfigPath)
	if err != nil {
		return err
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	tag := strings.TrimSpace(tagName)
	cfg.SetTagRoute(tag, targets)
	if err := config.Save(configPath, cfg); err != nil {
		return err
	}

	normalizedTag := strings.ToLower(strings.TrimSpace(tag))
	fmt.Printf("updated tag route %s\n", normalizedTag)
	fmt.Printf("channels: %s\n", formatConfigRouteTargets(cfg.TagRoutes[normalizedTag]))
	return nil
}

func runTagRouteRemove(globalConfigPath string, args []string) error {
	fs := flag.NewFlagSet("tag-route remove", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: ai-sched-cli tag-route remove <tag>")
	}

	configPath, err := resolveConfigPath(globalConfigPath)
	if err != nil {
		return err
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	tag := strings.TrimSpace(fs.Arg(0))
	if !cfg.RemoveTagRoute(tag) {
		return fmt.Errorf("tag route not found: %s", tag)
	}
	if err := config.Save(configPath, cfg); err != nil {
		return err
	}

	fmt.Printf("removed tag route %s\n", strings.ToLower(tag))
	return nil
}

func runDaemon(globalConfigPath string, args []string) error {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	once := fs.Bool("once", false, "process due tasks once and exit")
	ensure := fs.Bool("ensure", false, "start the daemon in the background if it is not already running")
	status := fs.Bool("status", false, "show daemon process status")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *ensure && *status {
		return fmt.Errorf("--ensure and --status cannot be used together")
	}
	if *ensure && *once {
		return fmt.Errorf("--ensure and --once cannot be used together")
	}
	if *status && *once {
		return fmt.Errorf("--status and --once cannot be used together")
	}
	if *ensure {
		return ensureDaemon(globalConfigPath)
	}
	if *status {
		return printDaemonStatus(globalConfigPath)
	}
	cleanupDaemon, err := activateDaemonProcessGuard()
	if err != nil {
		return err
	}
	defer cleanupDaemon()

	cfg, repo, cleanup, err := openRepository(globalConfigPath)
	if err != nil {
		return err
	}
	defer cleanup()

	logger := log.New(os.Stdout, logx.Name+" daemon: ", log.LstdFlags)
	runner := scheduler.Runner{
		Repo:    repo,
		Factory: channelFactory{config: cfg},
		Executor: executor.ACPXRunner{
			Command: cfg.AI.Command,
			Args:    append([]string(nil), cfg.AI.Args...),
		},
		PollInterval:        time.Duration(cfg.Daemon.PollIntervalSeconds) * time.Second,
		NotificationPoll:    time.Duration(cfg.Daemon.NotificationPollSeconds) * time.Second,
		StuckRunningTimeout: time.Duration(cfg.Daemon.StuckRunningTimeoutSeconds) * time.Second,
		AgentTimeout:        15 * time.Minute,
		NotificationRetries: cfg.Daemon.NotificationMaxRetries,
		Logger:              logger,
	}

	if *once {
		logger.Printf("running single scheduler pass")
		if err := runner.RunOnce(context.Background()); err != nil {
			return err
		}
		return runner.ProcessNotificationsOnce(context.Background())
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Printf("starting scheduler loop")
	err = runner.Run(ctx)
	if errors.Is(err, context.Canceled) {
		logger.Printf("scheduler stopped")
		return nil
	}
	return err
}

func openRepository(globalConfigPath string) (config.Config, *task.Repository, func(), error) {
	cfgPath, err := resolveConfigPath(globalConfigPath)
	if err != nil {
		return config.Config{}, nil, nil, err
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return config.Config{}, nil, nil, fmt.Errorf("config not initialized; run: ai-sched-cli init")
		}
		return config.Config{}, nil, nil, err
	}

	appDB, err := openDBFromConfig(cfg)
	if err != nil {
		return config.Config{}, nil, nil, err
	}

	cleanup := func() {
		_ = appDB.Close()
	}
	return cfg, task.NewRepository(appDB.SQL), cleanup, nil
}

func openDBFromConfig(cfg config.Config) (*db.DB, error) {
	dbPath, err := cfg.ResolvedDatabasePath()
	if err != nil {
		return nil, err
	}
	return db.Open(dbPath)
}

func resolveConfigPath(path string) (string, error) {
	if strings.TrimSpace(path) != "" {
		return path, nil
	}
	return config.DefaultPath()
}

func extractGlobalConfigPath(args []string) (string, []string, error) {
	if len(args) == 0 {
		return "", args, nil
	}
	if args[0] != "--config" {
		return "", args, nil
	}
	if len(args) < 2 {
		return "", nil, fmt.Errorf("--config requires a path")
	}
	return args[1], args[2:], nil
}

func extractLeadingTaskID(args []string) (string, []string, error) {
	if len(args) == 0 {
		return "", args, nil
	}
	if strings.HasPrefix(args[0], "-") {
		return "", args, nil
	}
	return args[0], args[1:], nil
}

func resolveTaskCWD(raw string) (string, error) {
	cwd := strings.TrimSpace(raw)
	var err error
	if cwd == "" {
		cwd, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get working directory: %w", err)
		}
	}

	cwd, err = filepath.Abs(cwd)
	if err != nil {
		return "", fmt.Errorf("resolve cwd: %w", err)
	}

	info, err := os.Stat(cwd)
	if err != nil {
		return "", fmt.Errorf("stat cwd: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("cwd is not a directory: %s", cwd)
	}
	return cwd, nil
}

func resolveSchedule(timezone, atArg, inArg, repeatRule string) (nextRunAt int64, runAt int64, scheduleType task.ScheduleType, timeOfDay string, err error) {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return 0, 0, "", "", fmt.Errorf("load timezone %q: %w", timezone, err)
	}

	hasAt := strings.TrimSpace(atArg) != ""
	hasIn := strings.TrimSpace(inArg) != ""
	if hasAt == hasIn {
		return 0, 0, "", "", fmt.Errorf("exactly one of --at or --in is required")
	}

	var scheduled time.Time
	switch {
	case hasAt:
		scheduled, err = parseScheduledTime(strings.TrimSpace(atArg), loc)
		if err != nil {
			return 0, 0, "", "", err
		}
	case hasIn:
		var duration time.Duration
		duration, err = time.ParseDuration(strings.TrimSpace(inArg))
		if err != nil {
			return 0, 0, "", "", fmt.Errorf("parse --in duration: %w", err)
		}
		scheduled = time.Now().In(loc).Add(duration)
	}

	if strings.TrimSpace(repeatRule) != "" {
		return scheduled.Unix(), 0, task.ScheduleRecurring, scheduled.Format("15:04:05"), nil
	}

	return scheduled.Unix(), scheduled.Unix(), task.ScheduleOnce, "", nil
}

func parseScheduledTime(raw string, loc *time.Location) (time.Time, error) {
	layouts := []string{
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
	}

	for _, layout := range layouts {
		if layout == time.RFC3339 {
			if ts, err := time.Parse(layout, raw); err == nil {
				return ts.In(loc), nil
			}
			continue
		}
		if ts, err := time.ParseInLocation(layout, raw, loc); err == nil {
			return ts, nil
		}
	}

	return time.Time{}, fmt.Errorf("unsupported time format: %s", raw)
}

func createTaskFromDraft(cfg config.Config, repo *task.Repository, rawInput string, draft ai.TaskDraft, tags []string) (task.Task, error) {
	nextRunAt, err := time.Parse(time.RFC3339, strings.TrimSpace(draft.NextRunAt))
	if err != nil {
		return task.Task{}, fmt.Errorf("parse ai next_run_at: %w", err)
	}

	scheduleType := task.ScheduleType(strings.TrimSpace(draft.ScheduleType))
	action := task.Action(strings.TrimSpace(draft.Action))
	if action == "" {
		action = task.ActionRunAgent
	}
	agent := strings.TrimSpace(draft.Agent)
	if action == task.ActionRunAgent && agent == "" {
		agent = "codex"
	}
	instruction := strings.TrimSpace(draft.Instruction)
	if instruction == "" {
		instruction = coalesceRawInput(strings.TrimSpace(draft.RawInput), strings.TrimSpace(draft.Summary))
	}
	runAt := int64(0)
	if scheduleType == task.ScheduleOnce {
		runAt = nextRunAt.Unix()
	}
	resolvedTags := append(append([]string(nil), draft.Tags...), tags...)
	notifyPolicy := task.NotifyPolicy(strings.TrimSpace(draft.NotifyPolicy))
	if notifyPolicy == "" {
		notifyPolicy = task.NotifyPolicyDefaultOn
	}
	taskChannels, err := resolveCreateChannels(cfg, singleValueOrNil(strings.TrimSpace(draft.Channel)), nil, resolvedTags, notifyPolicy != task.NotifyPolicyOff)
	if err != nil {
		return task.Task{}, err
	}

	return repo.Create(context.Background(), task.CreateInput{
		RawInput:      strings.TrimSpace(rawInput),
		Summary:       strings.TrimSpace(draft.Summary),
		Action:        action,
		Agent:         agent,
		Instruction:   instruction,
		Model:         strings.TrimSpace(draft.Model),
		ScheduleType:  scheduleType,
		Timezone:      strings.TrimSpace(draft.Timezone),
		RunAt:         runAt,
		RepeatRule:    strings.TrimSpace(draft.RepeatRule),
		TimeOfDay:     strings.TrimSpace(draft.TimeOfDay),
		NextRunAt:     nextRunAt.Unix(),
		CWD:           strings.TrimSpace(draft.CWD),
		NotifyPolicy:  notifyPolicy,
		Channel:       firstChannelName(taskChannels),
		ChannelRef:    firstChannelRef(taskChannels),
		Channels:      taskChannels,
		Tags:          resolvedTags,
		ConfirmStatus: task.ConfirmNone,
	})
}

func coalesceRawInput(rawInput, summary string) string {
	rawInput = strings.TrimSpace(rawInput)
	if rawInput != "" {
		return rawInput
	}
	return strings.TrimSpace(summary)
}

func applyTaskScheduleUpdate(existing task.Task, atArg, inArg, repeatArg, timeOfDayArg string) (task.Task, error) {
	updated := existing
	atArg = strings.TrimSpace(atArg)
	inArg = strings.TrimSpace(inArg)
	repeatArg = strings.TrimSpace(repeatArg)
	timeOfDayArg = strings.TrimSpace(timeOfDayArg)

	if atArg != "" || inArg != "" {
		nextRunAt, runAt, scheduleType, derivedTimeOfDay, err := resolveSchedule(existing.Timezone, atArg, inArg, repeatArg)
		if err != nil {
			return task.Task{}, err
		}
		if timeOfDayArg != "" {
			derivedTimeOfDay = timeOfDayArg
		}
		updated.ScheduleType = scheduleType
		updated.RunAt = runAt
		updated.RepeatRule = repeatArg
		updated.TimeOfDay = derivedTimeOfDay
		updated.NextRunAt = nextRunAt
		return updated, nil
	}

	repeatRule := repeatArg
	if repeatRule == "" {
		repeatRule = existing.RepeatRule
	}
	timeOfDay := timeOfDayArg
	if timeOfDay == "" {
		timeOfDay = existing.TimeOfDay
	}
	if repeatRule == "" {
		return task.Task{}, fmt.Errorf("schedule update requires --at/--in or a repeat rule")
	}
	if timeOfDay == "" {
		return task.Task{}, fmt.Errorf("recurring schedule update requires time of day")
	}

	now := time.Now()
	temp := existing
	temp.ScheduleType = task.ScheduleRecurring
	temp.RepeatRule = repeatRule
	temp.TimeOfDay = timeOfDay
	nextRunAt, err := task.NextRecurringRun(temp, now)
	if err != nil {
		return task.Task{}, err
	}

	updated.ScheduleType = task.ScheduleRecurring
	updated.RunAt = 0
	updated.RepeatRule = repeatRule
	updated.TimeOfDay = timeOfDay
	updated.NextRunAt = nextRunAt
	return updated, nil
}

func parseStatuses(raw string) ([]task.Status, error) {
	parts := strings.Split(raw, ",")
	out := make([]task.Status, 0, len(parts))
	for _, part := range parts {
		status := task.Status(strings.TrimSpace(part))
		switch status {
		case task.StatusPending, task.StatusRunning, task.StatusDone, task.StatusFailed, task.StatusCancelled:
			out = append(out, status)
		case "":
			continue
		default:
			return nil, fmt.Errorf("unsupported status: %s", part)
		}
	}
	return out, nil
}

func resolveCreateChannels(cfg config.Config, explicitChannels, refs, tags []string, allowNotify bool) ([]task.ChannelTarget, error) {
	if !allowNotify {
		if len(explicitChannels) > 0 || len(refs) > 0 {
			return nil, fmt.Errorf("channels cannot be set when notifications are disabled")
		}
		return nil, nil
	}
	if len(explicitChannels) > 0 || len(refs) > 0 {
		return resolveTaskChannels(explicitChannels, refs, cfg.DefaultChannel)
	}
	if routed := cfg.ResolveTagRouteTargets(tags); len(routed) > 0 {
		return configTargetsToTask(routed), nil
	}
	return resolveTaskChannels(nil, nil, cfg.DefaultChannel)
}

func resolveNotifyPolicy(notify, noNotify bool) (task.NotifyPolicy, error) {
	switch {
	case notify && noNotify:
		return "", fmt.Errorf("--notify and --no-notify cannot be used together")
	case noNotify:
		return task.NotifyPolicyOff, nil
	case notify:
		return task.NotifyPolicyExplicitOn, nil
	default:
		return task.NotifyPolicyDefaultOn, nil
	}
}

func firstChannelName(targets []task.ChannelTarget) string {
	if len(targets) == 0 {
		return ""
	}
	return targets[0].Channel
}

func firstChannelRef(targets []task.ChannelTarget) string {
	if len(targets) == 0 {
		return ""
	}
	return targets[0].ChannelRef
}

func resolveTaskChannels(channels, refs []string, defaultChannel string) ([]task.ChannelTarget, error) {
	resolvedChannels := append([]string(nil), channels...)
	if len(resolvedChannels) == 0 {
		defaultChannel = strings.TrimSpace(defaultChannel)
		switch {
		case defaultChannel != "":
			resolvedChannels = []string{defaultChannel}
		case len(refs) > 0:
			return nil, fmt.Errorf("--channel-ref requires matching --channel values")
		default:
			return nil, fmt.Errorf("at least one channel is required")
		}
	}
	if len(refs) > len(resolvedChannels) {
		return nil, fmt.Errorf("too many --channel-ref values for %d channel(s)", len(resolvedChannels))
	}

	targets := make([]task.ChannelTarget, 0, len(resolvedChannels))
	for index, channelName := range resolvedChannels {
		channelName = strings.TrimSpace(channelName)
		if channelName == "" {
			return nil, fmt.Errorf("channel cannot be empty")
		}
		ref := ""
		if index < len(refs) {
			ref = strings.TrimSpace(refs[index])
		}
		targets = append(targets, task.ChannelTarget{
			Channel:    channelName,
			ChannelRef: ref,
		})
	}
	return targets, nil
}

func resolveConfigRouteTargets(channels, refs []string, defaultChannel string) ([]config.TagRouteTarget, error) {
	taskTargets, err := resolveTaskChannels(channels, refs, defaultChannel)
	if err != nil {
		return nil, err
	}
	return taskTargetsToConfig(taskTargets), nil
}

func formatUnix(ts int64, timezone string) string {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.Local
	}
	return time.Unix(ts, 0).In(loc).Format("2006-01-02 15:04:05 MST")
}

func printUsage() {
	fmt.Println("ai-sched-cli commands:")
	fmt.Println("  ai-sched-cli init [--force]")
	fmt.Println("  ai-sched-cli add [--summary <text>] (--at <time> | --in <duration>) [--repeat <rule>] [--channel <name>] [--cwd <dir>] [--tag <name>] [text]")
	fmt.Println("  ai-sched-cli list [--all] [--status pending,running,failed] [--limit 50]")
	fmt.Println("  ai-sched-cli show <task-id>")
	fmt.Println("  ai-sched-cli runs [task-id] [--limit 20]")
	fmt.Println("  ai-sched-cli run-show <run-id>")
	fmt.Println("  ai-sched-cli remove <task-id>")
	fmt.Println("  ai-sched-cli run <task-id>")
	fmt.Println("  ai-sched-cli update <task-id> [flags]")
	fmt.Println("  ai-sched-cli status [--no-check]")
	fmt.Println("  ai-sched-cli tag-route <list|set|remove> [...]")
	fmt.Println("  ai-sched-cli daemon [--once|--ensure|--status]")
	fmt.Println("  ai-sched-cli version")
	fmt.Println("")
	fmt.Println("Global flags:")
	fmt.Println("  --config <path>  Override config path")
}

func formatMaybeUnix(ts int64, timezone string) string {
	if ts <= 0 {
		return "-"
	}
	return formatUnix(ts, timezone)
}

func truncateText(raw string, limit int) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "-"
	}
	if len(trimmed) <= limit {
		return trimmed
	}
	return trimmed[:limit] + "..."
}

func emptyDash(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "-"
	}
	return trimmed
}

func indentBlock(raw string) string {
	lines := strings.Split(raw, "\n")
	for i, line := range lines {
		lines[i] = "  " + line
	}
	return strings.Join(lines, "\n")
}

func formatTags(tags []string) string {
	if len(tags) == 0 {
		return "-"
	}
	return strings.Join(tags, ",")
}

func formatChannelTargets(targets []task.ChannelTarget) string {
	if len(targets) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(targets))
	for _, target := range targets {
		channelName := strings.TrimSpace(target.Channel)
		if channelName == "" {
			continue
		}
		ref := strings.TrimSpace(target.ChannelRef)
		if ref == "" {
			parts = append(parts, channelName)
			continue
		}
		parts = append(parts, channelName+":"+ref)
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ",")
}

func formatConfigRouteTargets(targets []config.TagRouteTarget) string {
	if len(targets) == 0 {
		return "-"
	}
	return formatChannelTargets(configTargetsToTask(targets))
}

func configTargetsToTask(targets []config.TagRouteTarget) []task.ChannelTarget {
	out := make([]task.ChannelTarget, 0, len(targets))
	for _, target := range targets {
		out = append(out, task.ChannelTarget{
			Channel:    strings.TrimSpace(target.Channel),
			ChannelRef: strings.TrimSpace(target.ChannelRef),
		})
	}
	return out
}

func taskTargetsToConfig(targets []task.ChannelTarget) []config.TagRouteTarget {
	out := make([]config.TagRouteTarget, 0, len(targets))
	for _, target := range targets {
		out = append(out, config.TagRouteTarget{
			Channel:    strings.TrimSpace(target.Channel),
			ChannelRef: strings.TrimSpace(target.ChannelRef),
		})
	}
	return out
}

func singleValueOrNil(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return []string{value}
}

type multiStringFlag struct {
	values []string
}

func (f *multiStringFlag) String() string {
	return strings.Join(f.values, ",")
}

func (f *multiStringFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		f.values = append(f.values, part)
	}
	return nil
}

func (f *multiStringFlag) Values() []string {
	return append([]string(nil), f.values...)
}
