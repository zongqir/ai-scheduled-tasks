package scheduler

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"ai-sched-cli/internal/channel"
	"ai-sched-cli/internal/executor"
	"ai-sched-cli/internal/task"
)

type SenderFactory interface {
	Resolve(name, ref string) (channel.Sender, error)
}

type Runner struct {
	Repo                *task.Repository
	Factory             SenderFactory
	Executor            executor.Runner
	PollInterval        time.Duration
	NotificationPoll    time.Duration
	StuckRunningTimeout time.Duration
	AgentTimeout        time.Duration
	NotificationRetries int
	Logger              *log.Logger
	Now                 func() time.Time
}

func (r Runner) Run(ctx context.Context) error {
	if err := r.validate(); err != nil {
		return err
	}

	taskTicker := time.NewTicker(r.PollInterval)
	defer taskTicker.Stop()
	notificationTicker := time.NewTicker(r.notificationPollInterval())
	defer notificationTicker.Stop()

	if err := r.RunOnce(ctx); err != nil {
		return err
	}
	if err := r.ProcessNotificationsOnce(ctx); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-taskTicker.C:
			if err := r.RunOnce(ctx); err != nil {
				return err
			}
		case <-notificationTicker.C:
			if err := r.ProcessNotificationsOnce(ctx); err != nil {
				return err
			}
		}
	}
}

func (r Runner) RunOnce(ctx context.Context) error {
	if err := r.validate(); err != nil {
		return err
	}

	if err := r.recoverStaleTasks(ctx); err != nil {
		return err
	}

	for {
		now := r.now()
		record, ok, err := r.Repo.ClaimDue(ctx, now.Unix())
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}

		if err := r.processTask(ctx, record, now); err != nil {
			r.logf("task %s failed: %v", record.ID, err)
		}
	}
}

func (r Runner) RunTaskByID(ctx context.Context, id string) error {
	if err := r.validate(); err != nil {
		return err
	}

	record, err := r.Repo.ClaimByID(ctx, id)
	if err != nil {
		return err
	}

	now := r.now()
	if err := r.processTask(ctx, record, now); err != nil {
		r.logf("task %s failed: %v", record.ID, err)
		return err
	}
	return nil
}

func (r Runner) ProcessNotificationsOnce(ctx context.Context) error {
	if err := r.validate(); err != nil {
		return err
	}

	for {
		notification, ok, err := r.Repo.ClaimNextNotification(ctx, r.now().Unix())
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if err := r.processNotification(ctx, notification); err != nil {
			r.logf("notification %s failed: %v", notification.ID, err)
		}
	}
}

func (r Runner) processTask(ctx context.Context, record task.Task, now time.Time) error {
	run, startErr := r.Repo.StartRun(ctx, record.ID, buildRunInput(record))
	if startErr != nil {
		return r.Repo.FinishRun(ctx, task.FinishRunInput{
			TaskID:         record.ID,
			RunStatus:      task.RunStatusFailed,
			TaskStatus:     task.StatusFailed,
			AIOutput:       "",
			ExecOutput:     "run initialization failed",
			Error:          fmt.Sprintf("start run: %v", startErr),
			ClearLastError: false,
		})
	}

	execOutput, notifyOutput, notifications, finishInput, execErr := r.executeTask(ctx, record, run.ID, now)
	if execErr != nil {
		finishInput.RunID = run.ID
		finishInput.TaskID = record.ID
		finishInput.AIOutput = ""
		finishInput.ExecOutput = execOutput
		finishInput.NotifyOutput = notifyOutput
		finishInput.Notifications = notifications
		if finishErr := r.Repo.FinishRun(ctx, finishInput); finishErr != nil {
			return fmt.Errorf("%v; finalize failure: %w", execErr, finishErr)
		}
		return execErr
	}

	finishInput.RunID = run.ID
	finishInput.TaskID = record.ID
	finishInput.AIOutput = ""
	finishInput.ExecOutput = execOutput
	finishInput.NotifyOutput = notifyOutput
	finishInput.Notifications = notifications
	if err := r.applyScheduleResult(record, now, &finishInput); err != nil {
		finishInput.RunStatus = task.RunStatusFailed
		finishInput.TaskStatus = task.StatusFailed
		finishInput.Error = err.Error()
		finishInput.ClearLastError = false
	}

	if err := r.Repo.FinishRun(ctx, finishInput); err != nil {
		return err
	}

	r.logf("task %s processed with action %s and status %s", record.ID, record.Action, finishInput.TaskStatus)
	return nil
}

func (r Runner) executeTask(ctx context.Context, record task.Task, runID string, now time.Time) (string, string, []task.CreateNotificationInput, task.FinishRunInput, error) {
	finishInput := task.FinishRunInput{
		RunID:          runID,
		TaskID:         record.ID,
		RunStatus:      task.RunStatusSuccess,
		TaskStatus:     task.StatusDone,
		AIOutput:       "",
		NotifyOutput:   "no notification queued",
		ClearLastError: true,
	}

	if err := executor.Validate(string(record.Action)); err != nil {
		finishInput.RunStatus = task.RunStatusFailed
		finishInput.TaskStatus = task.StatusFailed
		finishInput.Error = err.Error()
		finishInput.ClearLastError = false
		return "invalid ai action", finishInput.NotifyOutput, nil, finishInput, err
	}

	switch record.Action {
	case task.ActionRunAgent:
		result, err := r.Executor.RunAgent(ctx, executor.RunAgentInput{
			CWD:     record.CWD,
			Agent:   strings.TrimSpace(record.Agent),
			Prompt:  strings.TrimSpace(record.Instruction),
			Model:   strings.TrimSpace(record.Model),
			Timeout: r.agentTimeout(),
		})
		execOutput := summarizeExecution(record, result)
		notifications := buildResultNotifications(record, result, err)
		notifyOutput := notificationRunDetail(record, len(notifications))
		if err != nil {
			finishInput.RunStatus = task.RunStatusFailed
			finishInput.TaskStatus = task.StatusFailed
			finishInput.Error = fmt.Sprintf("acpx agent failed with exit code %d", result.ExitCode)
			finishInput.ClearLastError = false
			return execOutput, notifyOutput, notifications, finishInput, err
		}
		return execOutput, notifyOutput, notifications, finishInput, nil
	case task.ActionNotify:
		notifications := buildChannelNotifications(record, reminderNotificationBody(record))
		return "action=notify", notificationRunDetail(record, len(notifications)), notifications, finishInput, nil
	default:
		finishInput.RunStatus = task.RunStatusFailed
		finishInput.TaskStatus = task.StatusFailed
		finishInput.Error = "unsupported action"
		finishInput.ClearLastError = false
		return "unsupported action", finishInput.NotifyOutput, nil, finishInput, fmt.Errorf("unsupported action: %s", record.Action)
	}
}

func (r Runner) failWithoutRun(ctx context.Context, record task.Task, reason string) error {
	return r.Repo.FinishRun(ctx, task.FinishRunInput{
		TaskID:         record.ID,
		RunStatus:      task.RunStatusFailed,
		TaskStatus:     task.StatusFailed,
		ExecOutput:     "execution setup failed",
		Error:          reason,
		ClearLastError: false,
	})
}

func (r Runner) sendNotification(ctx context.Context, record task.Task, priority, message string) (*channel.SendResult, error) {
	sender, err := r.Factory.Resolve(record.Channel, record.ChannelRef)
	if err != nil {
		return nil, err
	}
	if err := sender.Check(ctx); err != nil {
		return nil, err
	}

	msg := channel.Message{
		Title:    record.Summary,
		Body:     strings.TrimSpace(message),
		Priority: strings.TrimSpace(priority),
	}
	if msg.Title == "" {
		msg.Title = "Scheduled task"
	}
	if msg.Priority == "" {
		msg.Priority = "normal"
	}

	return sender.Send(ctx, msg)
}

func (r Runner) processNotification(ctx context.Context, notification task.Notification) error {
	sender, err := r.Factory.Resolve(notification.Channel, notification.ChannelRef)
	if err != nil {
		return r.Repo.MarkNotificationRetry(ctx, notification.ID, err.Error(), r.notificationPollInterval(), r.notificationMaxRetries())
	}
	if err := sender.Check(ctx); err != nil {
		return r.Repo.MarkNotificationRetry(ctx, notification.ID, err.Error(), r.notificationPollInterval(), r.notificationMaxRetries())
	}

	result, err := sender.Send(ctx, channel.Message{
		Title:    notification.Title,
		Body:     notification.Body,
		Priority: notification.Priority,
	})
	if err != nil {
		return r.Repo.MarkNotificationRetry(ctx, notification.ID, err.Error(), r.notificationPollInterval(), r.notificationMaxRetries())
	}

	detail := ""
	if result != nil {
		detail = result.Detail
	}
	if err := r.Repo.MarkNotificationSent(ctx, notification.ID, detail); err != nil {
		return err
	}
	r.logf("notification %s delivered via %s", notification.ID, notification.Channel)
	return nil
}

func (r Runner) recoverStaleTasks(ctx context.Context) error {
	timeoutBefore := r.now().Add(-r.StuckRunningTimeout).Unix()
	affected, err := r.Repo.MarkStaleRunningFailed(ctx, timeoutBefore, "scheduler interrupted")
	if err != nil {
		return err
	}
	if affected > 0 {
		r.logf("recovered %d stale running tasks", affected)
	}
	return nil
}

func (r Runner) validate() error {
	if r.Repo == nil {
		return fmt.Errorf("scheduler repo is required")
	}
	if r.Factory == nil {
		return fmt.Errorf("scheduler sender factory is required")
	}
	if r.Executor == nil {
		return fmt.Errorf("scheduler executor is required")
	}
	if r.PollInterval <= 0 {
		return fmt.Errorf("scheduler poll interval must be > 0")
	}
	if r.notificationPollInterval() <= 0 {
		return fmt.Errorf("scheduler notification poll interval must be > 0")
	}
	if r.StuckRunningTimeout <= 0 {
		return fmt.Errorf("scheduler stuck running timeout must be > 0")
	}
	return nil
}

func (r Runner) agentTimeout() time.Duration {
	if r.AgentTimeout > 0 {
		return r.AgentTimeout
	}
	return 15 * time.Minute
}

func (r Runner) notificationPollInterval() time.Duration {
	if r.NotificationPoll > 0 {
		return r.NotificationPoll
	}
	return 10 * time.Second
}

func (r Runner) notificationMaxRetries() int {
	if r.NotificationRetries > 0 {
		return r.NotificationRetries
	}
	return 10
}

func (r Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r Runner) logf(format string, args ...any) {
	if r.Logger == nil {
		return
	}
	r.Logger.Printf(format, args...)
}

func (r Runner) applyScheduleResult(record task.Task, now time.Time, finishInput *task.FinishRunInput) error {
	if record.ScheduleType != task.ScheduleRecurring {
		return nil
	}
	nextRunAt, err := task.NextRecurringRun(record, now)
	if err != nil {
		return err
	}
	finishInput.TaskStatus = task.StatusPending
	finishInput.NextRunAt = nextRunAt
	return nil
}

func summarizeExecution(record task.Task, result executor.Result) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("action=%s", record.Action))
	if strings.TrimSpace(record.Agent) != "" {
		parts = append(parts, "agent="+strings.TrimSpace(record.Agent))
	}
	if len(result.Command) > 0 {
		parts = append(parts, "command="+strings.Join(result.Command, " "))
	}
	parts = append(parts, fmt.Sprintf("exit_code=%d", result.ExitCode))
	if result.Stdout != "" {
		parts = append(parts, "stdout="+truncate(result.Stdout, 2000))
	}
	if result.Stderr != "" {
		parts = append(parts, "stderr="+truncate(result.Stderr, 2000))
	}
	return strings.Join(parts, "\n")
}

func buildRunInput(record task.Task) string {
	var parts []string
	parts = append(parts, "action="+string(record.Action))
	if strings.TrimSpace(record.Agent) != "" {
		parts = append(parts, "agent="+strings.TrimSpace(record.Agent))
	}
	if strings.TrimSpace(record.Model) != "" {
		parts = append(parts, "model="+strings.TrimSpace(record.Model))
	}
	if strings.TrimSpace(record.CWD) != "" {
		parts = append(parts, "cwd="+strings.TrimSpace(record.CWD))
	}
	if strings.TrimSpace(record.Instruction) != "" {
		parts = append(parts, "instruction="+truncate(strings.TrimSpace(record.Instruction), 1000))
	}
	return strings.Join(parts, "\n")
}

func buildResultNotifications(record task.Task, result executor.Result, execErr error) []task.CreateNotificationInput {
	return buildChannelNotifications(record, resultNotificationBody(record, result, execErr))
}

func buildChannelNotifications(record task.Task, body string) []task.CreateNotificationInput {
	if !record.ShouldNotify() {
		return nil
	}
	targets := record.EffectiveChannels()
	notifications := make([]task.CreateNotificationInput, 0, len(targets))
	for _, target := range targets {
		notifications = append(notifications, task.CreateNotificationInput{
			Channel:    target.Channel,
			ChannelRef: target.ChannelRef,
			Title:      notificationTitle(record),
			Body:       body,
			Priority:   "normal",
		})
	}
	return notifications
}

func notificationTitle(record task.Task) string {
	summary := strings.TrimSpace(record.Summary)
	if summary != "" {
		return summary
	}
	if strings.TrimSpace(record.RawInput) != "" {
		return strings.TrimSpace(record.RawInput)
	}
	return "Scheduled task"
}

func resultNotificationBody(record task.Task, result executor.Result, execErr error) string {
	var parts []string
	if execErr != nil {
		parts = append(parts, "执行结果：失败")
	} else {
		parts = append(parts, "执行结果：成功")
	}
	parts = append(parts, "任务："+notificationTitle(record))
	parts = append(parts, "动作："+string(record.Action))
	if strings.TrimSpace(record.Agent) != "" {
		parts = append(parts, "Agent："+strings.TrimSpace(record.Agent))
	}
	if strings.TrimSpace(record.CWD) != "" {
		parts = append(parts, "目录："+strings.TrimSpace(record.CWD))
	}
	if len(record.Tags) > 0 {
		parts = append(parts, "标签："+strings.Join(record.Tags, ", "))
	}
	if strings.TrimSpace(record.Instruction) != "" {
		parts = append(parts, "")
		parts = append(parts, "执行内容：")
		parts = append(parts, truncate(strings.TrimSpace(record.Instruction), 500))
	}
	if execErr != nil {
		parts = append(parts, "")
		parts = append(parts, "错误信息：")
		parts = append(parts, truncate(strings.TrimSpace(execErr.Error()), 500))
	}
	if result.Stdout != "" {
		parts = append(parts, "")
		parts = append(parts, "输出结果：")
		parts = append(parts, truncate(strings.TrimSpace(result.Stdout), 1000))
	}
	if result.Stderr != "" {
		parts = append(parts, "")
		parts = append(parts, "错误输出：")
		parts = append(parts, truncate(strings.TrimSpace(result.Stderr), 1000))
	}
	return strings.Join(parts, "\n")
}

func reminderNotificationBody(record task.Task) string {
	var parts []string
	parts = append(parts, "提醒事项")
	parts = append(parts, "任务："+notificationTitle(record))
	if len(record.Tags) > 0 {
		parts = append(parts, "标签："+strings.Join(record.Tags, ", "))
	}
	if strings.TrimSpace(record.CWD) != "" {
		parts = append(parts, "目录："+strings.TrimSpace(record.CWD))
	}
	if strings.TrimSpace(record.Instruction) != "" {
		parts = append(parts, "")
		parts = append(parts, "内容：")
		parts = append(parts, truncate(strings.TrimSpace(record.Instruction), 1000))
	}
	return strings.Join(parts, "\n")
}

func queuedNotificationDetail(count int) string {
	if count <= 0 {
		return "no notification queued"
	}
	return fmt.Sprintf("queued %d notification(s)", count)
}

func notificationRunDetail(record task.Task, count int) string {
	if !record.ShouldNotify() {
		return "notifications disabled"
	}
	return queuedNotificationDetail(count)
}

func truncate(raw string, limit int) string {
	if len(raw) <= limit {
		return raw
	}
	return raw[:limit] + "...(truncated)"
}
