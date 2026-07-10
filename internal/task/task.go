package task

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusDone      Status = "done"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

type ScheduleType string

const (
	ScheduleOnce      ScheduleType = "once"
	ScheduleRecurring ScheduleType = "recurring"
)

type ConfirmStatus string

const (
	ConfirmNone      ConfirmStatus = "none"
	ConfirmRequired  ConfirmStatus = "required"
	ConfirmConfirmed ConfirmStatus = "confirmed"
)

type Action string

const (
	ActionRunAgent Action = "run_agent"
	ActionNotify   Action = "notify"
)

type Task struct {
	ID            string
	RawInput      string
	Summary       string
	Action        Action
	Agent         string
	Instruction   string
	Model         string
	ScheduleType  ScheduleType
	Timezone      string
	RunAt         int64
	RepeatRule    string
	TimeOfDay     string
	NextRunAt     int64
	CWD           string
	Channel       string
	ChannelRef    string
	Channels      []ChannelTarget
	Tags          []string
	Status        Status
	ConfirmStatus ConfirmStatus
	CreatedAt     int64
	UpdatedAt     int64
	LastError     string
}

type Run struct {
	ID           string
	TaskID       string
	StartedAt    int64
	FinishedAt   int64
	Status       RunStatus
	AIInput      string
	AIOutput     string
	ExecOutput   string
	NotifyOutput string
	Error        string
}

type RunStatus string

const (
	RunStatusRunning RunStatus = "running"
	RunStatusSuccess RunStatus = "success"
	RunStatusFailed  RunStatus = "failed"
)

type CreateInput struct {
	RawInput      string
	Summary       string
	Action        Action
	Agent         string
	Instruction   string
	Model         string
	ScheduleType  ScheduleType
	Timezone      string
	RunAt         int64
	RepeatRule    string
	TimeOfDay     string
	NextRunAt     int64
	CWD           string
	Channel       string
	ChannelRef    string
	Channels      []ChannelTarget
	Tags          []string
	ConfirmStatus ConfirmStatus
}

type ListFilter struct {
	Status []Status
	Limit  int
}

type RunListFilter struct {
	TaskID string
	Limit  int
}

type Stats struct {
	Total     int
	Pending   int
	Running   int
	Done      int
	Failed    int
	Cancelled int
}

type Repository struct {
	db *sql.DB
}

type UpdateInput struct {
	ID            string
	RawInput      string
	Summary       string
	Action        Action
	Agent         string
	Instruction   string
	Model         string
	ScheduleType  ScheduleType
	Timezone      string
	RunAt         int64
	RepeatRule    string
	TimeOfDay     string
	NextRunAt     int64
	CWD           string
	Channel       string
	ChannelRef    string
	Channels      []ChannelTarget
	Tags          []string
	ConfirmStatus ConfirmStatus
	Status        Status
}

type FinishRunInput struct {
	RunID          string
	TaskID         string
	RunStatus      RunStatus
	TaskStatus     Status
	NextRunAt      int64
	AIOutput       string
	ExecOutput     string
	NotifyOutput   string
	Notifications  []CreateNotificationInput
	Error          string
	ClearLastError bool
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Create(ctx context.Context, input CreateInput) (Task, error) {
	input = input.normalized()
	if err := input.Validate(); err != nil {
		return Task{}, err
	}

	now := time.Now().Unix()
	record := Task{
		ID:            newID(),
		RawInput:      strings.TrimSpace(input.RawInput),
		Summary:       strings.TrimSpace(input.Summary),
		Action:        input.Action,
		Agent:         strings.TrimSpace(input.Agent),
		Instruction:   strings.TrimSpace(input.Instruction),
		Model:         strings.TrimSpace(input.Model),
		ScheduleType:  input.ScheduleType,
		Timezone:      input.Timezone,
		RunAt:         input.RunAt,
		RepeatRule:    strings.TrimSpace(input.RepeatRule),
		TimeOfDay:     strings.TrimSpace(input.TimeOfDay),
		NextRunAt:     input.NextRunAt,
		CWD:           strings.TrimSpace(input.CWD),
		Channel:       strings.TrimSpace(input.Channel),
		ChannelRef:    strings.TrimSpace(input.ChannelRef),
		Channels:      normalizeChannelTargets(input.Channels),
		Tags:          normalizeTags(input.Tags),
		Status:        StatusPending,
		ConfirmStatus: input.ConfirmStatus,
		CreatedAt:     now,
		UpdatedAt:     now,
		LastError:     "",
	}

	if record.RawInput == "" {
		record.RawInput = record.Summary
	}
	if record.ConfirmStatus == "" {
		record.ConfirmStatus = ConfirmNone
	}

	const query = `
		INSERT INTO tasks (
			id, raw_input, summary, action, agent, instruction, model, schedule_type, timezone, run_at, repeat_rule,
			time_of_day, next_run_at, cwd, channel, channel_ref, tags, status,
			confirm_status, created_at, updated_at, last_error
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, fmt.Errorf("begin create task tx: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(
		ctx,
		query,
		record.ID,
		record.RawInput,
		record.Summary,
		record.Action,
		record.Agent,
		record.Instruction,
		nullIfEmpty(record.Model),
		record.ScheduleType,
		record.Timezone,
		nullIfZero(record.RunAt),
		nullIfEmpty(record.RepeatRule),
		nullIfEmpty(record.TimeOfDay),
		record.NextRunAt,
		record.CWD,
		record.Channel,
		nullIfEmpty(record.ChannelRef),
		nullIfEmpty(joinTags(record.Tags)),
		record.Status,
		record.ConfirmStatus,
		record.CreatedAt,
		record.UpdatedAt,
		nullIfEmpty(record.LastError),
	)
	if err != nil {
		return Task{}, fmt.Errorf("insert task: %w", err)
	}
	if err := insertTaskChannelsTx(tx, record.ID, record.Channels); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, fmt.Errorf("commit create task: %w", err)
	}
	return record, nil
}

func (r *Repository) Update(ctx context.Context, input UpdateInput) (Task, error) {
	if strings.TrimSpace(input.ID) == "" {
		return Task{}, fmt.Errorf("id is required")
	}

	createInput := CreateInput{
		RawInput:      input.RawInput,
		Summary:       input.Summary,
		Action:        input.Action,
		Agent:         input.Agent,
		Instruction:   input.Instruction,
		Model:         input.Model,
		ScheduleType:  input.ScheduleType,
		Timezone:      input.Timezone,
		RunAt:         input.RunAt,
		RepeatRule:    input.RepeatRule,
		TimeOfDay:     input.TimeOfDay,
		NextRunAt:     input.NextRunAt,
		CWD:           input.CWD,
		Channel:       input.Channel,
		ChannelRef:    input.ChannelRef,
		Channels:      input.Channels,
		Tags:          input.Tags,
		ConfirmStatus: input.ConfirmStatus,
	}.normalized()
	if err := createInput.Validate(); err != nil {
		return Task{}, err
	}

	now := time.Now().Unix()
	record := Task{
		ID:            strings.TrimSpace(input.ID),
		RawInput:      createInput.RawInput,
		Summary:       createInput.Summary,
		Action:        createInput.Action,
		Agent:         createInput.Agent,
		Instruction:   createInput.Instruction,
		Model:         createInput.Model,
		ScheduleType:  createInput.ScheduleType,
		Timezone:      createInput.Timezone,
		RunAt:         createInput.RunAt,
		RepeatRule:    createInput.RepeatRule,
		TimeOfDay:     createInput.TimeOfDay,
		NextRunAt:     createInput.NextRunAt,
		CWD:           createInput.CWD,
		Channel:       createInput.Channel,
		ChannelRef:    createInput.ChannelRef,
		Channels:      normalizeChannelTargets(createInput.Channels),
		Tags:          normalizeTags(createInput.Tags),
		Status:        input.Status,
		ConfirmStatus: createInput.ConfirmStatus,
		UpdatedAt:     now,
		LastError:     "",
	}
	if record.Status == "" {
		record.Status = StatusPending
	}
	if record.ConfirmStatus == "" {
		record.ConfirmStatus = ConfirmNone
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, fmt.Errorf("begin update task tx: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		UPDATE tasks
		SET raw_input = ?, summary = ?, action = ?, agent = ?, instruction = ?, model = ?, schedule_type = ?, timezone = ?, run_at = ?,
			repeat_rule = ?, time_of_day = ?, next_run_at = ?, cwd = ?, channel = ?,
			channel_ref = ?, tags = ?, status = ?, confirm_status = ?, updated_at = ?, last_error = NULL
		WHERE id = ?
	`,
		record.RawInput,
		record.Summary,
		record.Action,
		record.Agent,
		record.Instruction,
		nullIfEmpty(record.Model),
		record.ScheduleType,
		record.Timezone,
		nullIfZero(record.RunAt),
		nullIfEmpty(record.RepeatRule),
		nullIfEmpty(record.TimeOfDay),
		record.NextRunAt,
		record.CWD,
		record.Channel,
		nullIfEmpty(record.ChannelRef),
		nullIfEmpty(joinTags(record.Tags)),
		record.Status,
		record.ConfirmStatus,
		record.UpdatedAt,
		record.ID,
	)
	if err != nil {
		return Task{}, fmt.Errorf("update task: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return Task{}, fmt.Errorf("update task rows affected: %w", err)
	}
	if affected == 0 {
		return Task{}, fmt.Errorf("task not found: %s", record.ID)
	}
	if err := replaceTaskChannelsTx(tx, record.ID, record.Channels); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, fmt.Errorf("commit update task: %w", err)
	}

	stored, err := r.GetByID(ctx, record.ID)
	if err != nil {
		return Task{}, err
	}
	return stored, nil
}

func (r *Repository) List(ctx context.Context, filter ListFilter) ([]Task, error) {
	var (
		args  []any
		where []string
	)

	query := `
		SELECT
			id, raw_input, summary, action, agent, instruction, model, schedule_type, timezone, run_at, repeat_rule,
			time_of_day, next_run_at, cwd, channel, channel_ref, tags, status,
			confirm_status, created_at, updated_at, last_error
		FROM tasks
	`

	if len(filter.Status) > 0 {
		placeholders := make([]string, 0, len(filter.Status))
		for _, status := range filter.Status {
			placeholders = append(placeholders, "?")
			args = append(args, status)
		}
		where = append(where, "status IN ("+strings.Join(placeholders, ", ")+")")
	}

	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}

	query += " ORDER BY next_run_at ASC, created_at ASC"
	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query tasks: %w", err)
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		record, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, record)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tasks: %w", err)
	}
	if err := r.hydrateTaskChannels(ctx, tasks); err != nil {
		return nil, err
	}

	return tasks, nil
}

func (r *Repository) Delete(ctx context.Context, id string) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin delete task tx: %w", err)
	}
	defer tx.Rollback()

	trimmedID := strings.TrimSpace(id)
	if _, err := tx.ExecContext(ctx, `DELETE FROM runs WHERE task_id = ?`, trimmedID); err != nil {
		return false, fmt.Errorf("delete task runs: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM outbox_notifications WHERE task_id = ?`, trimmedID); err != nil {
		return false, fmt.Errorf("delete task notifications: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM task_channels WHERE task_id = ?`, trimmedID); err != nil {
		return false, fmt.Errorf("delete task channels: %w", err)
	}

	result, err := tx.ExecContext(ctx, `DELETE FROM tasks WHERE id = ?`, trimmedID)
	if err != nil {
		return false, fmt.Errorf("delete task: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("delete task rows affected: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit delete task: %w", err)
	}

	return affected > 0, nil
}

func (r *Repository) Stats(ctx context.Context) (Stats, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM tasks GROUP BY status`)
	if err != nil {
		return Stats{}, fmt.Errorf("query task stats: %w", err)
	}
	defer rows.Close()

	var stats Stats
	for rows.Next() {
		var (
			status string
			count  int
		)
		if err := rows.Scan(&status, &count); err != nil {
			return Stats{}, fmt.Errorf("scan task stats: %w", err)
		}

		stats.Total += count
		switch Status(status) {
		case StatusPending:
			stats.Pending = count
		case StatusRunning:
			stats.Running = count
		case StatusDone:
			stats.Done = count
		case StatusFailed:
			stats.Failed = count
		case StatusCancelled:
			stats.Cancelled = count
		}
	}

	if err := rows.Err(); err != nil {
		return Stats{}, fmt.Errorf("iterate task stats: %w", err)
	}

	return stats, nil
}

func (in CreateInput) Validate() error {
	if strings.TrimSpace(in.Summary) == "" {
		return fmt.Errorf("summary is required")
	}
	if in.Action == "" {
		in.Action = ActionRunAgent
	}
	switch in.Action {
	case ActionRunAgent:
		if strings.TrimSpace(in.Agent) == "" {
			return fmt.Errorf("agent is required for run_agent tasks")
		}
		if strings.TrimSpace(in.Instruction) == "" {
			return fmt.Errorf("instruction is required for run_agent tasks")
		}
	case ActionNotify:
		if strings.TrimSpace(in.Instruction) == "" {
			return fmt.Errorf("instruction is required for notify tasks")
		}
	default:
		return fmt.Errorf("unsupported action: %s", in.Action)
	}
	if strings.TrimSpace(in.Timezone) == "" {
		return fmt.Errorf("timezone is required")
	}
	if strings.TrimSpace(in.CWD) == "" {
		return fmt.Errorf("cwd is required")
	}
	if len(in.EffectiveChannels()) == 0 {
		return fmt.Errorf("at least one channel is required")
	}
	if in.NextRunAt <= 0 {
		return fmt.Errorf("next_run_at must be > 0")
	}
	if in.ScheduleType == "" {
		in.ScheduleType = ScheduleOnce
	}
	switch in.ScheduleType {
	case ScheduleOnce:
		if in.RunAt <= 0 {
			return fmt.Errorf("run_at must be > 0 for once tasks")
		}
	case ScheduleRecurring:
		if strings.TrimSpace(in.RepeatRule) == "" {
			return fmt.Errorf("repeat_rule is required for recurring tasks")
		}
	default:
		return fmt.Errorf("unsupported schedule_type: %s", in.ScheduleType)
	}
	return nil
}

func (in CreateInput) normalized() CreateInput {
	in.RawInput = strings.TrimSpace(in.RawInput)
	in.Summary = strings.TrimSpace(in.Summary)
	in.Action = Action(strings.TrimSpace(string(in.Action)))
	in.Agent = strings.TrimSpace(in.Agent)
	in.Instruction = strings.TrimSpace(in.Instruction)
	in.Model = strings.TrimSpace(in.Model)
	in.Timezone = strings.TrimSpace(in.Timezone)
	in.RepeatRule = strings.TrimSpace(in.RepeatRule)
	in.TimeOfDay = strings.TrimSpace(in.TimeOfDay)
	in.CWD = strings.TrimSpace(in.CWD)
	in.Channel = strings.TrimSpace(in.Channel)
	in.ChannelRef = strings.TrimSpace(in.ChannelRef)
	in.Channels = normalizeChannelTargets(in.Channels)
	in.Tags = normalizeTags(in.Tags)
	if in.ScheduleType == "" {
		in.ScheduleType = ScheduleOnce
	}
	if in.Action == "" {
		in.Action = ActionRunAgent
	}
	if in.Action == ActionRunAgent && in.Agent == "" {
		in.Agent = "codex"
	}
	if in.Instruction == "" {
		in.Instruction = in.RawInput
		if in.Instruction == "" {
			in.Instruction = in.Summary
		}
	}
	if in.ConfirmStatus == "" {
		in.ConfirmStatus = ConfirmNone
	}
	if in.RawInput == "" {
		in.RawInput = in.Summary
	}
	if len(in.Channels) == 0 && in.Channel != "" {
		in.Channels = []ChannelTarget{{
			Channel:    in.Channel,
			ChannelRef: in.ChannelRef,
		}}
	}
	if len(in.Channels) > 0 {
		in.Channel = in.Channels[0].Channel
		in.ChannelRef = in.Channels[0].ChannelRef
	}
	return in
}

func (r *Repository) ClaimDue(ctx context.Context, now int64) (Task, bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, false, fmt.Errorf("begin claim tx: %w", err)
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx, `
		SELECT
			id, raw_input, summary, action, agent, instruction, model, schedule_type, timezone, run_at, repeat_rule,
			time_of_day, next_run_at, cwd, channel, channel_ref, tags, status,
			confirm_status, created_at, updated_at, last_error
		FROM tasks
		WHERE status = ? AND next_run_at <= ?
		ORDER BY next_run_at ASC, created_at ASC
		LIMIT 1
	`, StatusPending, now)

	record, err := scanTask(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Task{}, false, nil
		}
		return Task{}, false, err
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE tasks
		SET status = ?, updated_at = ?, last_error = NULL
		WHERE id = ? AND status = ?
	`, StatusRunning, now, record.ID, StatusPending)
	if err != nil {
		return Task{}, false, fmt.Errorf("mark task running: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return Task{}, false, fmt.Errorf("claim task rows affected: %w", err)
	}
	if affected == 0 {
		return Task{}, false, nil
	}
	if err := hydrateTaskChannelsTx(ctx, tx, &record); err != nil {
		return Task{}, false, err
	}

	if err := tx.Commit(); err != nil {
		return Task{}, false, fmt.Errorf("commit claim task: %w", err)
	}

	record.Status = StatusRunning
	record.UpdatedAt = now
	record.LastError = ""
	return record, true, nil
}

func (r *Repository) ClaimByID(ctx context.Context, id string) (Task, error) {
	now := time.Now().Unix()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, fmt.Errorf("begin claim by id tx: %w", err)
	}
	defer tx.Rollback()

	trimmedID := strings.TrimSpace(id)
	row := tx.QueryRowContext(ctx, `
		SELECT
			id, raw_input, summary, action, agent, instruction, model, schedule_type, timezone, run_at, repeat_rule,
			time_of_day, next_run_at, cwd, channel, channel_ref, tags, status,
			confirm_status, created_at, updated_at, last_error
		FROM tasks
		WHERE id = ?
	`, trimmedID)

	record, err := scanTask(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Task{}, fmt.Errorf("task not found: %s", trimmedID)
		}
		return Task{}, err
	}
	if record.Status == StatusRunning {
		return Task{}, fmt.Errorf("task is already running: %s", trimmedID)
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE tasks
		SET status = ?, updated_at = ?, last_error = NULL
		WHERE id = ?
	`, StatusRunning, now, trimmedID)
	if err != nil {
		return Task{}, fmt.Errorf("mark task running by id: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Task{}, fmt.Errorf("commit claim by id: %w", err)
	}
	records := []Task{record}
	if err := r.hydrateTaskChannels(ctx, records); err != nil {
		return Task{}, err
	}
	record = records[0]

	record.Status = StatusRunning
	record.UpdatedAt = now
	record.LastError = ""
	return record, nil
}

func (r *Repository) GetByID(ctx context.Context, id string) (Task, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT
			id, raw_input, summary, action, agent, instruction, model, schedule_type, timezone, run_at, repeat_rule,
			time_of_day, next_run_at, cwd, channel, channel_ref, tags, status,
			confirm_status, created_at, updated_at, last_error
		FROM tasks
		WHERE id = ?
	`, strings.TrimSpace(id))
	record, err := scanTask(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Task{}, fmt.Errorf("task not found: %s", strings.TrimSpace(id))
		}
		return Task{}, err
	}
	records := []Task{record}
	if err := r.hydrateTaskChannels(ctx, records); err != nil {
		return Task{}, err
	}
	record = records[0]
	return record, nil
}

func (r *Repository) ListRuns(ctx context.Context, filter RunListFilter) ([]Run, error) {
	var (
		args  []any
		where []string
	)

	query := `
		SELECT
			id, task_id, started_at, finished_at, status, ai_input, ai_output,
			exec_output, notify_output, error
		FROM runs
	`

	if strings.TrimSpace(filter.TaskID) != "" {
		where = append(where, "task_id = ?")
		args = append(args, strings.TrimSpace(filter.TaskID))
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}

	query += " ORDER BY started_at DESC, rowid DESC"
	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query runs: %w", err)
	}
	defer rows.Close()

	var runs []Run
	for rows.Next() {
		record, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate runs: %w", err)
	}
	return runs, nil
}

func (r *Repository) GetRunByID(ctx context.Context, id string) (Run, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT
			id, task_id, started_at, finished_at, status, ai_input, ai_output,
			exec_output, notify_output, error
		FROM runs
		WHERE id = ?
	`, strings.TrimSpace(id))

	record, err := scanRun(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Run{}, fmt.Errorf("run not found: %s", strings.TrimSpace(id))
		}
		return Run{}, err
	}
	return record, nil
}

func (r *Repository) MarkStaleRunningFailed(ctx context.Context, timeoutBefore int64, reason string) (int64, error) {
	now := time.Now().Unix()
	result, err := r.db.ExecContext(ctx, `
		UPDATE tasks
		SET status = ?, updated_at = ?, last_error = ?
		WHERE status = ? AND updated_at < ?
	`, StatusFailed, now, strings.TrimSpace(reason), StatusRunning, timeoutBefore)
	if err != nil {
		return 0, fmt.Errorf("mark stale running tasks failed: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("mark stale running rows affected: %w", err)
	}
	return affected, nil
}

func (r *Repository) StartRun(ctx context.Context, taskID string, aiInput string) (Run, error) {
	now := time.Now().Unix()
	record := Run{
		ID:         newRunID(),
		TaskID:     strings.TrimSpace(taskID),
		StartedAt:  now,
		Status:     RunStatusRunning,
		AIInput:    strings.TrimSpace(aiInput),
		FinishedAt: 0,
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO runs (
			id, task_id, started_at, finished_at, status, ai_input, ai_output,
			exec_output, notify_output, error
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		record.ID,
		record.TaskID,
		record.StartedAt,
		nil,
		record.Status,
		nullIfEmpty(record.AIInput),
		nil,
		nil,
		nil,
		nil,
	)
	if err != nil {
		return Run{}, fmt.Errorf("insert run: %w", err)
	}

	return record, nil
}

func (r *Repository) FinishRun(ctx context.Context, input FinishRunInput) error {
	now := time.Now().Unix()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin finish run tx: %w", err)
	}
	defer tx.Rollback()

	if strings.TrimSpace(input.RunID) != "" {
		_, err = tx.ExecContext(ctx, `
			UPDATE runs
			SET finished_at = ?, status = ?, ai_output = ?, exec_output = ?, notify_output = ?, error = ?
			WHERE id = ?
		`,
			now,
			input.RunStatus,
			nullIfEmpty(input.AIOutput),
			nullIfEmpty(input.ExecOutput),
			nullIfEmpty(input.NotifyOutput),
			nullIfEmpty(input.Error),
			strings.TrimSpace(input.RunID),
		)
		if err != nil {
			return fmt.Errorf("update run: %w", err)
		}
	}

	for _, notification := range input.Notifications {
		if _, err := insertNotificationTx(tx, strings.TrimSpace(input.TaskID), strings.TrimSpace(input.RunID), notification); err != nil {
			return err
		}
	}

	lastError := any(nil)
	if !input.ClearLastError {
		lastError = nullIfEmpty(input.Error)
	}

	if input.NextRunAt > 0 {
		_, err = tx.ExecContext(ctx, `
			UPDATE tasks
			SET status = ?, next_run_at = ?, updated_at = ?, last_error = ?
			WHERE id = ?
		`,
			input.TaskStatus,
			input.NextRunAt,
			now,
			lastError,
			strings.TrimSpace(input.TaskID),
		)
	} else {
		_, err = tx.ExecContext(ctx, `
			UPDATE tasks
			SET status = ?, updated_at = ?, last_error = ?
			WHERE id = ?
		`,
			input.TaskStatus,
			now,
			lastError,
			strings.TrimSpace(input.TaskID),
		)
	}
	if err != nil {
		return fmt.Errorf("update task after run: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit finish run: %w", err)
	}
	return nil
}

func NextRecurringRun(record Task, after time.Time) (int64, error) {
	if record.ScheduleType != ScheduleRecurring {
		return 0, fmt.Errorf("task %s is not recurring", record.ID)
	}

	loc, err := time.LoadLocation(record.Timezone)
	if err != nil {
		return 0, fmt.Errorf("load timezone %q: %w", record.Timezone, err)
	}

	timeOfDay, err := parseTimeOfDay(strings.TrimSpace(record.TimeOfDay))
	if err != nil {
		return 0, err
	}

	localAfter := after.In(loc)
	switch {
	case record.RepeatRule == "daily":
		candidate := atTimeOfDay(localAfter, timeOfDay)
		if !candidate.After(localAfter) {
			candidate = candidate.AddDate(0, 0, 1)
		}
		return candidate.Unix(), nil
	case strings.HasPrefix(record.RepeatRule, "weekly"):
		days, err := weeklyDays(record)
		if err != nil {
			return 0, err
		}
		for offset := 0; offset <= 7; offset++ {
			candidateDate := localAfter.AddDate(0, 0, offset)
			if !days[candidateDate.Weekday()] {
				continue
			}
			candidate := time.Date(
				candidateDate.Year(),
				candidateDate.Month(),
				candidateDate.Day(),
				timeOfDay.Hour,
				timeOfDay.Minute,
				timeOfDay.Second,
				0,
				loc,
			)
			if candidate.After(localAfter) {
				return candidate.Unix(), nil
			}
		}
		return 0, fmt.Errorf("could not compute next weekly run for task %s", record.ID)
	default:
		return 0, fmt.Errorf("unsupported repeat_rule: %s", record.RepeatRule)
	}
}

func scanTask(scanner interface{ Scan(dest ...any) error }) (Task, error) {
	var (
		record      Task
		action      sql.NullString
		agent       sql.NullString
		instruction sql.NullString
		model       sql.NullString
		runAt       sql.NullInt64
		repeatRule  sql.NullString
		timeOfDay   sql.NullString
		channelRef  sql.NullString
		tags        sql.NullString
		lastError   sql.NullString
	)

	if err := scanner.Scan(
		&record.ID,
		&record.RawInput,
		&record.Summary,
		&action,
		&agent,
		&instruction,
		&model,
		&record.ScheduleType,
		&record.Timezone,
		&runAt,
		&repeatRule,
		&timeOfDay,
		&record.NextRunAt,
		&record.CWD,
		&record.Channel,
		&channelRef,
		&tags,
		&record.Status,
		&record.ConfirmStatus,
		&record.CreatedAt,
		&record.UpdatedAt,
		&lastError,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Task{}, sql.ErrNoRows
		}
		return Task{}, fmt.Errorf("scan task: %w", err)
	}

	if runAt.Valid {
		record.RunAt = runAt.Int64
	}
	if action.Valid {
		record.Action = Action(action.String)
	}
	if agent.Valid {
		record.Agent = agent.String
	}
	if instruction.Valid {
		record.Instruction = instruction.String
	}
	if model.Valid {
		record.Model = model.String
	}
	if repeatRule.Valid {
		record.RepeatRule = repeatRule.String
	}
	if timeOfDay.Valid {
		record.TimeOfDay = timeOfDay.String
	}
	if channelRef.Valid {
		record.ChannelRef = channelRef.String
	}
	if tags.Valid {
		record.Tags = splitTags(tags.String)
	}
	if lastError.Valid {
		record.LastError = lastError.String
	}
	if record.Action == "" {
		record.Action = ActionRunAgent
	}
	if record.Action == ActionRunAgent && record.Agent == "" {
		record.Agent = "codex"
	}
	if record.Instruction == "" {
		record.Instruction = strings.TrimSpace(record.RawInput)
		if record.Instruction == "" {
			record.Instruction = strings.TrimSpace(record.Summary)
		}
	}

	return record, nil
}

func scanRun(scanner interface{ Scan(dest ...any) error }) (Run, error) {
	var (
		record       Run
		finishedAt   sql.NullInt64
		aiInput      sql.NullString
		aiOutput     sql.NullString
		execOutput   sql.NullString
		notifyOutput sql.NullString
		runError     sql.NullString
	)

	if err := scanner.Scan(
		&record.ID,
		&record.TaskID,
		&record.StartedAt,
		&finishedAt,
		&record.Status,
		&aiInput,
		&aiOutput,
		&execOutput,
		&notifyOutput,
		&runError,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Run{}, sql.ErrNoRows
		}
		return Run{}, fmt.Errorf("scan run: %w", err)
	}

	if finishedAt.Valid {
		record.FinishedAt = finishedAt.Int64
	}
	if aiInput.Valid {
		record.AIInput = aiInput.String
	}
	if aiOutput.Valid {
		record.AIOutput = aiOutput.String
	}
	if execOutput.Valid {
		record.ExecOutput = execOutput.String
	}
	if notifyOutput.Valid {
		record.NotifyOutput = notifyOutput.String
	}
	if runError.Valid {
		record.Error = runError.String
	}

	return record, nil
}

func newID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("task_%d", time.Now().UnixNano())
	}
	return "task_" + hex.EncodeToString(buf)
}

func newRunID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("run_%d", time.Now().UnixNano())
	}
	return "run_" + hex.EncodeToString(buf)
}

type clockTime struct {
	Hour   int
	Minute int
	Second int
}

func parseTimeOfDay(raw string) (clockTime, error) {
	layouts := []string{"15:04:05", "15:04"}
	for _, layout := range layouts {
		if ts, err := time.Parse(layout, raw); err == nil {
			return clockTime{
				Hour:   ts.Hour(),
				Minute: ts.Minute(),
				Second: ts.Second(),
			}, nil
		}
	}
	return clockTime{}, fmt.Errorf("unsupported time_of_day: %s", raw)
}

func atTimeOfDay(base time.Time, tod clockTime) time.Time {
	return time.Date(base.Year(), base.Month(), base.Day(), tod.Hour, tod.Minute, tod.Second, 0, base.Location())
}

func weeklyDays(record Task) (map[time.Weekday]bool, error) {
	parts := strings.SplitN(record.RepeatRule, "|", 2)
	rawDays := ""
	if len(parts) == 2 {
		rawDays = strings.TrimSpace(parts[1])
	}
	if rawDays == "" {
		base := time.Unix(record.NextRunAt, 0)
		if record.RunAt > 0 {
			base = time.Unix(record.RunAt, 0)
		}
		loc, err := time.LoadLocation(record.Timezone)
		if err != nil {
			return nil, fmt.Errorf("load timezone %q: %w", record.Timezone, err)
		}
		return map[time.Weekday]bool{base.In(loc).Weekday(): true}, nil
	}

	lookup := map[string]time.Weekday{
		"sun": time.Sunday,
		"mon": time.Monday,
		"tue": time.Tuesday,
		"wed": time.Wednesday,
		"thu": time.Thursday,
		"fri": time.Friday,
		"sat": time.Saturday,
	}

	days := make(map[time.Weekday]bool)
	for _, token := range strings.Split(rawDays, ",") {
		weekday, ok := lookup[strings.ToLower(strings.TrimSpace(token))]
		if !ok {
			return nil, fmt.Errorf("unsupported weekly day: %s", token)
		}
		days[weekday] = true
	}
	if len(days) == 0 {
		return nil, fmt.Errorf("weekly repeat_rule requires at least one day")
	}
	return days, nil
}

func nullIfEmpty(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

func nullIfZero(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func normalizeTags(tags []string) []string {
	seen := make(map[string]struct{}, len(tags))
	var out []string
	for _, tag := range tags {
		for _, part := range strings.Split(tag, ",") {
			trimmed := strings.TrimSpace(part)
			if trimmed == "" {
				continue
			}
			if _, ok := seen[trimmed]; ok {
				continue
			}
			seen[trimmed] = struct{}{}
			out = append(out, trimmed)
		}
	}
	return out
}

func joinTags(tags []string) string {
	return strings.Join(normalizeTags(tags), ",")
}

func splitTags(raw string) []string {
	return normalizeTags(strings.Split(raw, ","))
}
