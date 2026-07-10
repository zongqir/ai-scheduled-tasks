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

type NotificationStatus string

const (
	NotificationPending NotificationStatus = "pending"
	NotificationSending NotificationStatus = "sending"
	NotificationSent    NotificationStatus = "sent"
	NotificationFailed  NotificationStatus = "failed"
)

type Notification struct {
	ID             string
	TaskID         string
	RunID          string
	Channel        string
	ChannelRef     string
	Title          string
	Body           string
	Priority       string
	Status         NotificationStatus
	RetryCount     int
	NextRetryAt    int64
	LastError      string
	DeliveryDetail string
	CreatedAt      int64
	UpdatedAt      int64
	SentAt         int64
}

type CreateNotificationInput struct {
	Channel    string
	ChannelRef string
	Title      string
	Body       string
	Priority   string
}

func (in CreateNotificationInput) normalized() CreateNotificationInput {
	in.Channel = strings.TrimSpace(in.Channel)
	in.ChannelRef = strings.TrimSpace(in.ChannelRef)
	in.Title = strings.TrimSpace(in.Title)
	in.Body = strings.TrimSpace(in.Body)
	in.Priority = strings.TrimSpace(in.Priority)
	if in.Title == "" {
		in.Title = "Scheduled task"
	}
	return in
}

func (in CreateNotificationInput) Validate() error {
	if strings.TrimSpace(in.Channel) == "" {
		return fmt.Errorf("notification channel is required")
	}
	if strings.TrimSpace(in.Title) == "" {
		return fmt.Errorf("notification title is required")
	}
	return nil
}

func (r *Repository) ClaimNextNotification(ctx context.Context, now int64) (Notification, bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Notification{}, false, fmt.Errorf("begin claim notification tx: %w", err)
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx, `
		SELECT
			id, task_id, run_id, channel, channel_ref, title, body, priority, status,
			retry_count, next_retry_at, last_error, delivery_detail, created_at, updated_at, sent_at
		FROM outbox_notifications
		WHERE status = ? AND next_retry_at <= ?
		ORDER BY next_retry_at ASC, created_at ASC
		LIMIT 1
	`, NotificationPending, now)

	record, err := scanNotification(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Notification{}, false, nil
		}
		return Notification{}, false, err
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE outbox_notifications
		SET status = ?, updated_at = ?, last_error = NULL
		WHERE id = ? AND status = ?
	`, NotificationSending, now, record.ID, NotificationPending)
	if err != nil {
		return Notification{}, false, fmt.Errorf("mark notification sending: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return Notification{}, false, fmt.Errorf("claim notification rows affected: %w", err)
	}
	if affected == 0 {
		return Notification{}, false, nil
	}

	if err := tx.Commit(); err != nil {
		return Notification{}, false, fmt.Errorf("commit claim notification: %w", err)
	}

	record.Status = NotificationSending
	record.UpdatedAt = now
	record.LastError = ""
	return record, true, nil
}

func (r *Repository) MarkNotificationSent(ctx context.Context, id string, detail string) error {
	now := time.Now().Unix()
	_, err := r.db.ExecContext(ctx, `
		UPDATE outbox_notifications
		SET status = ?, delivery_detail = ?, updated_at = ?, sent_at = ?, last_error = NULL
		WHERE id = ?
	`, NotificationSent, nullIfEmpty(detail), now, now, strings.TrimSpace(id))
	if err != nil {
		return fmt.Errorf("mark notification sent: %w", err)
	}
	return nil
}

func (r *Repository) MarkNotificationRetry(ctx context.Context, id string, sendErr string, retryDelay time.Duration, maxRetries int) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin notification retry tx: %w", err)
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx, `
		SELECT retry_count
		FROM outbox_notifications
		WHERE id = ?
	`, strings.TrimSpace(id))

	var retryCount int
	if err := row.Scan(&retryCount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("notification not found: %s", strings.TrimSpace(id))
		}
		return fmt.Errorf("load notification retry count: %w", err)
	}

	now := time.Now().Unix()
	nextRetryAt := time.Now().Add(retryDelay).Unix()
	nextStatus := NotificationPending
	nextRetryCount := retryCount + 1
	if nextRetryCount >= maxRetries {
		nextStatus = NotificationFailed
		nextRetryAt = 0
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE outbox_notifications
		SET status = ?, retry_count = ?, next_retry_at = ?, last_error = ?, updated_at = ?
		WHERE id = ?
	`, nextStatus, nextRetryCount, nextRetryAt, nullIfEmpty(sendErr), now, strings.TrimSpace(id))
	if err != nil {
		return fmt.Errorf("update notification retry state: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit notification retry state: %w", err)
	}
	return nil
}

func (r *Repository) ListNotifications(ctx context.Context, limit int) ([]Notification, error) {
	query := `
		SELECT
			id, task_id, run_id, channel, channel_ref, title, body, priority, status,
			retry_count, next_retry_at, last_error, delivery_detail, created_at, updated_at, sent_at
		FROM outbox_notifications
		ORDER BY created_at DESC, rowid DESC
	`
	var args []any
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query notifications: %w", err)
	}
	defer rows.Close()

	var notifications []Notification
	for rows.Next() {
		record, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		notifications = append(notifications, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notifications: %w", err)
	}
	return notifications, nil
}

func insertNotificationTx(tx *sql.Tx, taskID, runID string, input CreateNotificationInput) (Notification, error) {
	input = input.normalized()
	if err := input.Validate(); err != nil {
		return Notification{}, err
	}

	now := time.Now().Unix()
	record := Notification{
		ID:          newNotificationID(),
		TaskID:      strings.TrimSpace(taskID),
		RunID:       strings.TrimSpace(runID),
		Channel:     input.Channel,
		ChannelRef:  input.ChannelRef,
		Title:       input.Title,
		Body:        input.Body,
		Priority:    input.Priority,
		Status:      NotificationPending,
		RetryCount:  0,
		NextRetryAt: now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	_, err := tx.Exec(`
		INSERT INTO outbox_notifications (
			id, task_id, run_id, channel, channel_ref, title, body, priority, status,
			retry_count, next_retry_at, last_error, delivery_detail, created_at, updated_at, sent_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		record.ID,
		record.TaskID,
		nullIfEmpty(record.RunID),
		record.Channel,
		nullIfEmpty(record.ChannelRef),
		record.Title,
		record.Body,
		nullIfEmpty(record.Priority),
		record.Status,
		record.RetryCount,
		record.NextRetryAt,
		nil,
		nil,
		record.CreatedAt,
		record.UpdatedAt,
		nil,
	)
	if err != nil {
		return Notification{}, fmt.Errorf("insert notification: %w", err)
	}
	return record, nil
}

func scanNotification(scanner interface{ Scan(dest ...any) error }) (Notification, error) {
	var (
		record         Notification
		runID          sql.NullString
		channelRef     sql.NullString
		priority       sql.NullString
		lastError      sql.NullString
		deliveryDetail sql.NullString
		sentAt         sql.NullInt64
	)

	if err := scanner.Scan(
		&record.ID,
		&record.TaskID,
		&runID,
		&record.Channel,
		&channelRef,
		&record.Title,
		&record.Body,
		&priority,
		&record.Status,
		&record.RetryCount,
		&record.NextRetryAt,
		&lastError,
		&deliveryDetail,
		&record.CreatedAt,
		&record.UpdatedAt,
		&sentAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Notification{}, sql.ErrNoRows
		}
		return Notification{}, fmt.Errorf("scan notification: %w", err)
	}

	if runID.Valid {
		record.RunID = runID.String
	}
	if channelRef.Valid {
		record.ChannelRef = channelRef.String
	}
	if priority.Valid {
		record.Priority = priority.String
	}
	if lastError.Valid {
		record.LastError = lastError.String
	}
	if deliveryDetail.Valid {
		record.DeliveryDetail = deliveryDetail.String
	}
	if sentAt.Valid {
		record.SentAt = sentAt.Int64
	}
	return record, nil
}

func newNotificationID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("notify_%d", time.Now().UnixNano())
	}
	return "notify_" + hex.EncodeToString(buf)
}
