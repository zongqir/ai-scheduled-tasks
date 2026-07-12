package scheduler

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"ai-sched-cli/internal/channel"
	"ai-sched-cli/internal/db"
	"ai-sched-cli/internal/executor"
	"ai-sched-cli/internal/task"
)

func TestRunnerRunOnceProcessesDueTaskWithAgentExecution(t *testing.T) {
	repo := newTestRepository(t)
	record, err := repo.Create(context.Background(), task.CreateInput{
		Summary:      "check CI",
		Action:       task.ActionRunAgent,
		Agent:        "codex",
		Instruction:  "fix it",
		ScheduleType: task.ScheduleOnce,
		Timezone:     "Asia/Shanghai",
		RunAt:        1,
		NextRunAt:    1,
		CWD:          "/tmp/project",
		Channel:      "webhook",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	sender := &fakeSender{}
	execRunner := &fakeExecutor{
		result: executor.Result{
			Command:  []string{"codex", "exec", "fix it"},
			Stdout:   "done",
			ExitCode: 0,
		},
	}
	runner := Runner{
		Repo:                repo,
		Factory:             fakeFactory{sender: sender},
		Executor:            execRunner,
		PollInterval:        time.Second,
		StuckRunningTimeout: 5 * time.Minute,
		AgentTimeout:        time.Minute,
		Now: func() time.Time {
			return time.Unix(2, 0)
		},
	}

	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("run once: %v", err)
	}

	stored, err := repo.GetByID(context.Background(), record.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if stored.Status != task.StatusDone {
		t.Fatalf("expected task status %q, got %q", task.StatusDone, stored.Status)
	}
	if len(sender.messages) != 0 {
		t.Fatalf("expected no message, got %d", len(sender.messages))
	}
	if len(execRunner.inputs) != 1 {
		t.Fatalf("expected 1 executor call, got %d", len(execRunner.inputs))
	}
	if execRunner.inputs[0].Prompt != "fix it" {
		t.Fatalf("unexpected executor prompt: %q", execRunner.inputs[0].Prompt)
	}
}

func TestRunnerRunOnceReschedulesRecurringTask(t *testing.T) {
	repo := newTestRepository(t)
	loc := time.FixedZone("CST", 8*3600)
	runAt := time.Date(2026, 7, 12, 18, 0, 0, 0, loc).Unix()

	record, err := repo.Create(context.Background(), task.CreateInput{
		Summary:      "daily reminder",
		Action:       task.ActionNotify,
		Instruction:  "daily reminder",
		ScheduleType: task.ScheduleRecurring,
		Timezone:     "Asia/Shanghai",
		RepeatRule:   "daily",
		TimeOfDay:    "18:00:00",
		NextRunAt:    runAt,
		CWD:          "/tmp/project",
		Channel:      "webhook",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	sender := &fakeSender{}
	now := time.Date(2026, 7, 12, 18, 0, 1, 0, loc)
	runner := Runner{
		Repo:                repo,
		Factory:             fakeFactory{sender: sender},
		Executor:            &fakeExecutor{},
		PollInterval:        time.Second,
		StuckRunningTimeout: 5 * time.Minute,
		Now: func() time.Time {
			return now
		},
	}

	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("run once: %v", err)
	}

	stored, err := repo.GetByID(context.Background(), record.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if stored.Status != task.StatusPending {
		t.Fatalf("expected task status %q, got %q", task.StatusPending, stored.Status)
	}

	next := time.Unix(stored.NextRunAt, 0).In(loc)
	if got := next.Format("2006-01-02 15:04:05"); got != "2026-07-13 18:00:00" {
		t.Fatalf("unexpected next run: %s", got)
	}
}

func TestRunnerRunOnceFailsWhenAgentExecutionFails(t *testing.T) {
	repo := newTestRepository(t)
	record, err := repo.Create(context.Background(), task.CreateInput{
		Summary:      "failed agent",
		Action:       task.ActionRunAgent,
		Agent:        "codex",
		Instruction:  "fail",
		ScheduleType: task.ScheduleOnce,
		Timezone:     "Asia/Shanghai",
		RunAt:        1,
		NextRunAt:    1,
		CWD:          "/tmp/project",
		Channel:      "webhook",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	sender := &fakeSender{}
	execRunner := &fakeExecutor{err: fmt.Errorf("agent failed")}
	runner := Runner{
		Repo:                repo,
		Factory:             fakeFactory{sender: sender},
		Executor:            execRunner,
		PollInterval:        time.Second,
		StuckRunningTimeout: 5 * time.Minute,
		Now: func() time.Time {
			return time.Unix(2, 0)
		},
	}

	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("run once: %v", err)
	}

	stored, err := repo.GetByID(context.Background(), record.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if stored.Status != task.StatusFailed {
		t.Fatalf("expected task status %q, got %q", task.StatusFailed, stored.Status)
	}
	if len(sender.messages) != 0 {
		t.Fatalf("expected no notification, got %d", len(sender.messages))
	}
	if stored.LastError == "" {
		t.Fatal("expected task error")
	}
}

func TestRunnerProcessNotificationsOnceDeliversQueuedResults(t *testing.T) {
	repo := newTestRepository(t)
	_, err := repo.Create(context.Background(), task.CreateInput{
		Summary:      "callback result",
		Action:       task.ActionRunAgent,
		Agent:        "codex",
		Instruction:  "Reply with OK only.",
		ScheduleType: task.ScheduleOnce,
		Timezone:     "Asia/Shanghai",
		RunAt:        1,
		NextRunAt:    1,
		CWD:          "/tmp/project",
		Channel:      "webhook",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	sender := &fakeSender{}
	execRunner := &fakeExecutor{
		result: executor.Result{
			Command:  []string{"acpx", "codex", "exec", "Reply with OK only."},
			Stdout:   "OK",
			ExitCode: 0,
		},
	}
	now := time.Now()
	runner := Runner{
		Repo:                repo,
		Factory:             fakeFactory{sender: sender},
		Executor:            execRunner,
		PollInterval:        time.Second,
		NotificationPoll:    10 * time.Second,
		StuckRunningTimeout: 5 * time.Minute,
		NotificationRetries: 3,
		Now: func() time.Time {
			return now
		},
	}

	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("run once: %v", err)
	}
	if len(sender.messages) != 0 {
		t.Fatalf("expected no synchronous send, got %d", len(sender.messages))
	}

	if err := runner.ProcessNotificationsOnce(context.Background()); err != nil {
		t.Fatalf("process notifications once: %v", err)
	}
	if len(sender.messages) != 1 {
		t.Fatalf("expected 1 delivered notification, got %d", len(sender.messages))
	}
	if sender.messages[0].Title != "callback result" {
		t.Fatalf("unexpected notification title: %q", sender.messages[0].Title)
	}

	notifications, err := repo.ListNotifications(context.Background(), 10)
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if len(notifications) != 1 {
		t.Fatalf("expected 1 notification row, got %d", len(notifications))
	}
	if notifications[0].Status != task.NotificationSent {
		t.Fatalf("expected sent notification, got %q", notifications[0].Status)
	}
}

func TestRunnerRunOnceFansOutNotificationsAcrossChannels(t *testing.T) {
	repo := newTestRepository(t)
	_, err := repo.Create(context.Background(), task.CreateInput{
		Summary:      "multi notify",
		Action:       task.ActionRunAgent,
		Agent:        "codex",
		Instruction:  "Reply with done.",
		ScheduleType: task.ScheduleOnce,
		Timezone:     "Asia/Shanghai",
		RunAt:        1,
		NextRunAt:    1,
		CWD:          "/tmp/project",
		Channels: []task.ChannelTarget{
			{Channel: "wecom_robot", ChannelRef: "ops"},
			{Channel: "wechat"},
		},
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	sender := &fakeSender{}
	execRunner := &fakeExecutor{
		result: executor.Result{
			Command:  []string{"acpx", "codex", "exec", "Reply with done."},
			Stdout:   "done",
			ExitCode: 0,
		},
	}
	now := time.Now()
	runner := Runner{
		Repo:                repo,
		Factory:             fakeFactory{sender: sender},
		Executor:            execRunner,
		PollInterval:        time.Second,
		NotificationPoll:    10 * time.Second,
		StuckRunningTimeout: 5 * time.Minute,
		NotificationRetries: 3,
		Now: func() time.Time {
			return now
		},
	}

	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("run once: %v", err)
	}
	if err := runner.ProcessNotificationsOnce(context.Background()); err != nil {
		t.Fatalf("process notifications once: %v", err)
	}
	if len(sender.messages) != 2 {
		t.Fatalf("expected 2 delivered notifications, got %d", len(sender.messages))
	}

	notifications, err := repo.ListNotifications(context.Background(), 10)
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if len(notifications) != 2 {
		t.Fatalf("expected 2 notification rows, got %d", len(notifications))
	}
	if notifications[0].Channel == notifications[1].Channel {
		t.Fatalf("expected fan-out across distinct channels, got %#v", notifications)
	}
}

func TestRunnerRunOnceSkipsOutboxWhenNotificationsDisabled(t *testing.T) {
	repo := newTestRepository(t)
	_, err := repo.Create(context.Background(), task.CreateInput{
		Summary:      "silent run",
		Action:       task.ActionRunAgent,
		Agent:        "codex",
		Instruction:  "Reply with done.",
		ScheduleType: task.ScheduleOnce,
		Timezone:     "Asia/Shanghai",
		RunAt:        1,
		NextRunAt:    1,
		CWD:          "/tmp/project",
		NotifyPolicy: task.NotifyPolicyOff,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	sender := &fakeSender{}
	execRunner := &fakeExecutor{
		result: executor.Result{
			Command:  []string{"acpx", "codex", "exec", "Reply with done."},
			Stdout:   "done",
			ExitCode: 0,
		},
	}
	runner := Runner{
		Repo:                repo,
		Factory:             fakeFactory{sender: sender},
		Executor:            execRunner,
		PollInterval:        time.Second,
		NotificationPoll:    10 * time.Second,
		StuckRunningTimeout: 5 * time.Minute,
		NotificationRetries: 3,
		Now: func() time.Time {
			return time.Unix(2, 0)
		},
	}

	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("run once: %v", err)
	}
	if err := runner.ProcessNotificationsOnce(context.Background()); err != nil {
		t.Fatalf("process notifications once: %v", err)
	}
	if len(sender.messages) != 0 {
		t.Fatalf("expected no delivered notifications, got %d", len(sender.messages))
	}

	notifications, err := repo.ListNotifications(context.Background(), 10)
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if len(notifications) != 0 {
		t.Fatalf("expected no notification rows, got %#v", notifications)
	}

	runs, err := repo.ListRuns(context.Background(), task.RunListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 || runs[0].NotifyOutput != "notifications disabled" {
		t.Fatalf("unexpected run notify output: %#v", runs)
	}
}

func TestResultNotificationBodyUsesReadableTemplate(t *testing.T) {
	body := resultNotificationBody(task.Task{
		Summary:     "检查 CI",
		Action:      task.ActionRunAgent,
		Agent:       "codex",
		CWD:         "/tmp/project",
		Instruction: "修复失败测试",
		Tags:        []string{"work", "urgent"},
	}, executor.Result{
		Stdout: "done",
		Stderr: "warn",
	}, nil)

	for _, expected := range []string{
		"执行结果：成功",
		"任务：检查 CI",
		"动作：run_agent",
		"Agent：codex",
		"目录：/tmp/project",
		"标签：work, urgent",
		"执行内容：",
		"输出结果：",
		"错误输出：",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected %q in body: %q", expected, body)
		}
	}
}

func TestReminderNotificationBodyUsesReadableTemplate(t *testing.T) {
	body := reminderNotificationBody(task.Task{
		Summary:     "买菜",
		Instruction: "下班后买牛奶",
		Tags:        []string{"personal"},
		CWD:         "/tmp/life",
	})

	for _, expected := range []string{
		"提醒事项",
		"任务：买菜",
		"标签：personal",
		"目录：/tmp/life",
		"内容：",
		"下班后买牛奶",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected %q in body: %q", expected, body)
		}
	}
}

type fakeExecutor struct {
	inputs []executor.RunAgentInput
	result executor.Result
	err    error
}

func (f *fakeExecutor) RunAgent(_ context.Context, input executor.RunAgentInput) (executor.Result, error) {
	f.inputs = append(f.inputs, input)
	return f.result, f.err
}

type fakeFactory struct {
	sender channel.Sender
}

func (f fakeFactory) Resolve(name, ref string) (channel.Sender, error) {
	if f.sender == nil {
		return nil, fmt.Errorf("sender missing")
	}
	return f.sender, nil
}

type fakeSender struct {
	messages []channel.Message
}

func (s *fakeSender) Name() string {
	return "fake"
}

func (s *fakeSender) Check(context.Context) error {
	return nil
}

func (s *fakeSender) Send(_ context.Context, msg channel.Message) (*channel.SendResult, error) {
	s.messages = append(s.messages, msg)
	return &channel.SendResult{
		Provider: "fake",
		Detail:   "sent",
	}, nil
}

func newTestRepository(t *testing.T) *task.Repository {
	t.Helper()

	appDB, err := db.Open(t.TempDir() + "/tasks.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if err := appDB.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}
	})

	return task.NewRepository(appDB.SQL)
}
