package task

import (
	"context"
	"testing"
	"time"

	"ai-sched-cli/internal/db"
)

func TestRepositoryCreateDefaultsScheduleType(t *testing.T) {
	repo := newTestRepository(t)

	record, err := repo.Create(context.Background(), CreateInput{
		Summary:    "check CI",
		Timezone:   "Asia/Shanghai",
		RunAt:      1,
		NextRunAt:  1,
		CWD:        "/tmp/project",
		Channel:    "webhook",
		ChannelRef: "demo",
		Tags:       []string{"work", "ci"},
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	if record.ScheduleType != ScheduleOnce {
		t.Fatalf("expected %q schedule type, got %q", ScheduleOnce, record.ScheduleType)
	}
	if record.RawInput != record.Summary {
		t.Fatalf("expected raw input to default to summary, got %q", record.RawInput)
	}
	if record.ConfirmStatus != ConfirmNone {
		t.Fatalf("expected confirm status %q, got %q", ConfirmNone, record.ConfirmStatus)
	}
	if record.NotifyPolicy != NotifyPolicyDefaultOn {
		t.Fatalf("expected notify policy %q, got %q", NotifyPolicyDefaultOn, record.NotifyPolicy)
	}
	if len(record.Tags) != 2 || record.Tags[0] != "work" || record.Tags[1] != "ci" {
		t.Fatalf("unexpected tags: %#v", record.Tags)
	}
}

func TestRepositoryCreateAllowsSilentTaskWithoutChannels(t *testing.T) {
	repo := newTestRepository(t)

	record, err := repo.Create(context.Background(), CreateInput{
		Summary:      "silent check",
		NotifyPolicy: NotifyPolicyOff,
		ScheduleType: ScheduleOnce,
		Timezone:     "Asia/Shanghai",
		RunAt:        1,
		NextRunAt:    1,
		CWD:          "/tmp/project",
	})
	if err != nil {
		t.Fatalf("create silent task: %v", err)
	}

	if record.NotifyPolicy != NotifyPolicyOff {
		t.Fatalf("expected notify policy off, got %q", record.NotifyPolicy)
	}
	if len(record.EffectiveChannels()) != 0 {
		t.Fatalf("expected no channels, got %#v", record.EffectiveChannels())
	}
}

func TestRepositoryStatsCountsCreatedTasks(t *testing.T) {
	repo := newTestRepository(t)

	for _, summary := range []string{"first", "second"} {
		_, err := repo.Create(context.Background(), CreateInput{
			Summary:      summary,
			ScheduleType: ScheduleOnce,
			Timezone:     "Asia/Shanghai",
			RunAt:        1,
			NextRunAt:    1,
			CWD:          "/tmp/project",
			Channel:      "webhook",
		})
		if err != nil {
			t.Fatalf("create task %q: %v", summary, err)
		}
	}

	stats, err := repo.Stats(context.Background())
	if err != nil {
		t.Fatalf("stats: %v", err)
	}

	if stats.Total != 2 || stats.Pending != 2 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestRepositoryClaimDueMarksTaskRunning(t *testing.T) {
	repo := newTestRepository(t)

	record, err := repo.Create(context.Background(), CreateInput{
		Summary:      "due task",
		ScheduleType: ScheduleOnce,
		Timezone:     "Asia/Shanghai",
		RunAt:        1,
		NextRunAt:    1,
		CWD:          "/tmp/project",
		Channel:      "webhook",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	claimed, ok, err := repo.ClaimDue(context.Background(), 2)
	if err != nil {
		t.Fatalf("claim due: %v", err)
	}
	if !ok {
		t.Fatal("expected task to be claimed")
	}
	if claimed.ID != record.ID {
		t.Fatalf("expected claimed task %q, got %q", record.ID, claimed.ID)
	}

	stored, err := repo.GetByID(context.Background(), record.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if stored.Status != StatusRunning {
		t.Fatalf("expected task status %q, got %q", StatusRunning, stored.Status)
	}
}

func TestRepositoryClaimByIDMarksTaskRunning(t *testing.T) {
	repo := newTestRepository(t)

	record, err := repo.Create(context.Background(), CreateInput{
		Summary:      "manual run",
		ScheduleType: ScheduleOnce,
		Timezone:     "Asia/Shanghai",
		RunAt:        1,
		NextRunAt:    999,
		CWD:          "/tmp/project",
		Channel:      "webhook",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	claimed, err := repo.ClaimByID(context.Background(), record.ID)
	if err != nil {
		t.Fatalf("claim by id: %v", err)
	}
	if claimed.ID != record.ID {
		t.Fatalf("expected claimed task %q, got %q", record.ID, claimed.ID)
	}
	if claimed.Status != StatusRunning {
		t.Fatalf("expected running status, got %q", claimed.Status)
	}
	if len(claimed.EffectiveChannels()) != 1 || claimed.EffectiveChannels()[0].Channel != "webhook" {
		t.Fatalf("unexpected claimed channels: %#v", claimed.EffectiveChannels())
	}
}

func TestRepositoryPersistsMultiChannels(t *testing.T) {
	repo := newTestRepository(t)

	record, err := repo.Create(context.Background(), CreateInput{
		Summary:      "fan out",
		ScheduleType: ScheduleOnce,
		Timezone:     "Asia/Shanghai",
		RunAt:        1,
		NextRunAt:    1,
		CWD:          "/tmp/project",
		Channels: []ChannelTarget{
			{Channel: "wecom_robot", ChannelRef: "team-a"},
			{Channel: "wechat"},
		},
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	if got := len(record.EffectiveChannels()); got != 2 {
		t.Fatalf("expected 2 channels on create, got %d", got)
	}
	if record.Channel != "wecom_robot" || record.ChannelRef != "team-a" {
		t.Fatalf("unexpected primary channel: %s %s", record.Channel, record.ChannelRef)
	}

	stored, err := repo.GetByID(context.Background(), record.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got := len(stored.EffectiveChannels()); got != 2 {
		t.Fatalf("expected 2 stored channels, got %d", got)
	}
	if stored.EffectiveChannels()[1].Channel != "wechat" {
		t.Fatalf("unexpected stored channels: %#v", stored.EffectiveChannels())
	}

	listed, err := repo.List(context.Background(), ListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(listed) != 1 || len(listed[0].EffectiveChannels()) != 2 {
		t.Fatalf("unexpected listed channels: %#v", listed)
	}
}

func TestRepositoryUpdateReplacesChannels(t *testing.T) {
	repo := newTestRepository(t)

	record, err := repo.Create(context.Background(), CreateInput{
		Summary:      "replace channels",
		ScheduleType: ScheduleOnce,
		Timezone:     "Asia/Shanghai",
		RunAt:        1,
		NextRunAt:    1,
		CWD:          "/tmp/project",
		Channels: []ChannelTarget{
			{Channel: "webhook", ChannelRef: "first"},
			{Channel: "wechat"},
		},
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	updated, err := repo.Update(context.Background(), UpdateInput{
		ID:           record.ID,
		RawInput:     record.RawInput,
		Summary:      record.Summary,
		Action:       record.Action,
		Agent:        record.Agent,
		Instruction:  record.Instruction,
		Model:        record.Model,
		ScheduleType: record.ScheduleType,
		Timezone:     record.Timezone,
		RunAt:        record.RunAt,
		RepeatRule:   record.RepeatRule,
		TimeOfDay:    record.TimeOfDay,
		NextRunAt:    record.NextRunAt,
		CWD:          record.CWD,
		Channels: []ChannelTarget{
			{Channel: "dida", ChannelRef: "project-1"},
		},
		Tags:          record.Tags,
		ConfirmStatus: ConfirmNone,
		Status:        StatusPending,
	})
	if err != nil {
		t.Fatalf("update task: %v", err)
	}

	if got := len(updated.EffectiveChannels()); got != 1 {
		t.Fatalf("expected 1 updated channel, got %d", got)
	}
	if updated.Channel != "dida" || updated.ChannelRef != "project-1" {
		t.Fatalf("unexpected updated primary channel: %s %s", updated.Channel, updated.ChannelRef)
	}
}

func TestNextRecurringRunDaily(t *testing.T) {
	record := Task{
		ID:           "task_daily",
		ScheduleType: ScheduleRecurring,
		Timezone:     "Asia/Shanghai",
		RepeatRule:   "daily",
		TimeOfDay:    "18:00:00",
	}

	after := time.Date(2026, 7, 12, 18, 30, 0, 0, time.FixedZone("CST", 8*3600))
	nextRunAt, err := NextRecurringRun(record, after)
	if err != nil {
		t.Fatalf("next recurring run: %v", err)
	}

	next := time.Unix(nextRunAt, 0).In(after.Location())
	if got := next.Format("2006-01-02 15:04:05"); got != "2026-07-13 18:00:00" {
		t.Fatalf("unexpected next run: %s", got)
	}
}

func TestNextRecurringRunWeekly(t *testing.T) {
	record := Task{
		ID:           "task_weekly",
		ScheduleType: ScheduleRecurring,
		Timezone:     "Asia/Shanghai",
		RepeatRule:   "weekly|mon,wed",
		TimeOfDay:    "09:00:00",
	}

	after := time.Date(2026, 7, 14, 10, 0, 0, 0, time.FixedZone("CST", 8*3600))
	nextRunAt, err := NextRecurringRun(record, after)
	if err != nil {
		t.Fatalf("next recurring run: %v", err)
	}

	next := time.Unix(nextRunAt, 0).In(after.Location())
	if got := next.Format("2006-01-02 15:04:05"); got != "2026-07-15 09:00:00" {
		t.Fatalf("unexpected next run: %s", got)
	}
}

func TestRepositoryListRunsReturnsRecentFirst(t *testing.T) {
	repo := newTestRepository(t)

	record, err := repo.Create(context.Background(), CreateInput{
		Summary:      "history task",
		ScheduleType: ScheduleOnce,
		Timezone:     "Asia/Shanghai",
		RunAt:        1,
		NextRunAt:    1,
		CWD:          "/tmp/project",
		Channel:      "webhook",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	run1, err := repo.StartRun(context.Background(), record.ID, "input1")
	if err != nil {
		t.Fatalf("start run1: %v", err)
	}
	if err := repo.FinishRun(context.Background(), FinishRunInput{
		RunID:          run1.ID,
		TaskID:         record.ID,
		RunStatus:      RunStatusSuccess,
		TaskStatus:     StatusDone,
		AIOutput:       "out1",
		ExecOutput:     "exec1",
		NotifyOutput:   "notify1",
		ClearLastError: true,
	}); err != nil {
		t.Fatalf("finish run1: %v", err)
	}

	run2, err := repo.StartRun(context.Background(), record.ID, "input2")
	if err != nil {
		t.Fatalf("start run2: %v", err)
	}
	if err := repo.FinishRun(context.Background(), FinishRunInput{
		RunID:          run2.ID,
		TaskID:         record.ID,
		RunStatus:      RunStatusFailed,
		TaskStatus:     StatusFailed,
		AIOutput:       "out2",
		ExecOutput:     "exec2",
		NotifyOutput:   "notify2",
		Error:          "boom",
		ClearLastError: false,
	}); err != nil {
		t.Fatalf("finish run2: %v", err)
	}

	runs, err := repo.ListRuns(context.Background(), RunListFilter{TaskID: record.ID, Limit: 10})
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(runs))
	}
	if runs[0].ID != run2.ID {
		t.Fatalf("expected newest run first: %q != %q", runs[0].ID, run2.ID)
	}
	if runs[0].Error != "boom" {
		t.Fatalf("expected run error to be preserved, got %q", runs[0].Error)
	}
}

func TestRepositoryUpdateReplacesTags(t *testing.T) {
	repo := newTestRepository(t)

	record, err := repo.Create(context.Background(), CreateInput{
		Summary:      "tagged task",
		ScheduleType: ScheduleOnce,
		Timezone:     "Asia/Shanghai",
		RunAt:        1,
		NextRunAt:    1,
		CWD:          "/tmp/project",
		Channel:      "webhook",
		Tags:         []string{"old"},
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	updated, err := repo.Update(context.Background(), UpdateInput{
		ID:            record.ID,
		RawInput:      record.RawInput,
		Summary:       record.Summary,
		ScheduleType:  record.ScheduleType,
		Timezone:      record.Timezone,
		RunAt:         record.RunAt,
		RepeatRule:    record.RepeatRule,
		TimeOfDay:     record.TimeOfDay,
		NextRunAt:     record.NextRunAt,
		CWD:           record.CWD,
		Channel:       record.Channel,
		ChannelRef:    record.ChannelRef,
		Tags:          []string{"new", "ops"},
		ConfirmStatus: ConfirmNone,
		Status:        StatusPending,
	})
	if err != nil {
		t.Fatalf("update task: %v", err)
	}

	if len(updated.Tags) != 2 || updated.Tags[0] != "new" || updated.Tags[1] != "ops" {
		t.Fatalf("unexpected updated tags: %#v", updated.Tags)
	}
}

func TestRepositoryGetRunByIDReturnsStoredFields(t *testing.T) {
	repo := newTestRepository(t)

	record, err := repo.Create(context.Background(), CreateInput{
		Summary:      "show run details",
		ScheduleType: ScheduleOnce,
		Timezone:     "Asia/Shanghai",
		RunAt:        1,
		NextRunAt:    1,
		CWD:          "/tmp/project",
		Channel:      "webhook",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	run, err := repo.StartRun(context.Background(), record.ID, "ai input")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if err := repo.FinishRun(context.Background(), FinishRunInput{
		RunID:          run.ID,
		TaskID:         record.ID,
		RunStatus:      RunStatusSuccess,
		TaskStatus:     StatusDone,
		AIOutput:       "{\"action\":\"notify\"}",
		ExecOutput:     "action=notify",
		NotifyOutput:   "sent",
		ClearLastError: true,
	}); err != nil {
		t.Fatalf("finish run: %v", err)
	}

	stored, err := repo.GetRunByID(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("get run by id: %v", err)
	}
	if stored.AIInput != "ai input" {
		t.Fatalf("unexpected ai input: %q", stored.AIInput)
	}
	if stored.AIOutput != "{\"action\":\"notify\"}" {
		t.Fatalf("unexpected ai output: %q", stored.AIOutput)
	}
	if stored.NotifyOutput != "sent" {
		t.Fatalf("unexpected notify output: %q", stored.NotifyOutput)
	}
}

func TestRepositoryFinishRunEnqueuesNotificationAndMarksSent(t *testing.T) {
	repo := newTestRepository(t)

	record, err := repo.Create(context.Background(), CreateInput{
		Summary:      "notify queue",
		ScheduleType: ScheduleOnce,
		Timezone:     "Asia/Shanghai",
		RunAt:        1,
		NextRunAt:    1,
		CWD:          "/tmp/project",
		Channel:      "webhook",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	run, err := repo.StartRun(context.Background(), record.ID, "ai input")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if err := repo.FinishRun(context.Background(), FinishRunInput{
		RunID:        run.ID,
		TaskID:       record.ID,
		RunStatus:    RunStatusSuccess,
		TaskStatus:   StatusDone,
		ExecOutput:   "done",
		NotifyOutput: "queued 1 notification(s)",
		Notifications: []CreateNotificationInput{{
			Channel:  "webhook",
			Title:    "notify queue",
			Body:     "completed",
			Priority: "normal",
		}},
		ClearLastError: true,
	}); err != nil {
		t.Fatalf("finish run: %v", err)
	}

	notification, ok, err := repo.ClaimNextNotification(context.Background(), time.Now().Unix())
	if err != nil {
		t.Fatalf("claim notification: %v", err)
	}
	if !ok {
		t.Fatal("expected pending notification")
	}
	if notification.RunID != run.ID {
		t.Fatalf("unexpected run id: %q", notification.RunID)
	}
	if notification.Status != NotificationSending {
		t.Fatalf("expected sending status, got %q", notification.Status)
	}

	if err := repo.MarkNotificationSent(context.Background(), notification.ID, "delivered"); err != nil {
		t.Fatalf("mark sent: %v", err)
	}

	notifications, err := repo.ListNotifications(context.Background(), 10)
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if len(notifications) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifications))
	}
	if notifications[0].Status != NotificationSent {
		t.Fatalf("expected sent status, got %q", notifications[0].Status)
	}
	if notifications[0].DeliveryDetail != "delivered" {
		t.Fatalf("unexpected delivery detail: %q", notifications[0].DeliveryDetail)
	}
}

func newTestRepository(t *testing.T) *Repository {
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

	return NewRepository(appDB.SQL)
}
