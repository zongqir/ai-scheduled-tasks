package task

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

type ChannelTarget struct {
	Channel    string
	ChannelRef string
}

func (t Task) EffectiveChannels() []ChannelTarget {
	if len(t.Channels) > 0 {
		return append([]ChannelTarget(nil), t.Channels...)
	}
	if strings.TrimSpace(t.Channel) == "" {
		return nil
	}
	return []ChannelTarget{{
		Channel:    strings.TrimSpace(t.Channel),
		ChannelRef: strings.TrimSpace(t.ChannelRef),
	}}
}

func (in CreateInput) EffectiveChannels() []ChannelTarget {
	if len(in.Channels) > 0 {
		return append([]ChannelTarget(nil), normalizeChannelTargets(in.Channels)...)
	}
	if strings.TrimSpace(in.Channel) == "" {
		return nil
	}
	return []ChannelTarget{{
		Channel:    strings.TrimSpace(in.Channel),
		ChannelRef: strings.TrimSpace(in.ChannelRef),
	}}
}

func normalizeChannelTargets(targets []ChannelTarget) []ChannelTarget {
	if len(targets) == 0 {
		return nil
	}

	normalized := make([]ChannelTarget, 0, len(targets))
	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		channel := strings.TrimSpace(target.Channel)
		channelRef := strings.TrimSpace(target.ChannelRef)
		if channel == "" {
			continue
		}
		key := channel + "\x00" + channelRef
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, ChannelTarget{
			Channel:    channel,
			ChannelRef: channelRef,
		})
	}
	return normalized
}

func insertTaskChannelsTx(tx *sql.Tx, taskID string, targets []ChannelTarget) error {
	for index, target := range normalizeChannelTargets(targets) {
		if _, err := tx.Exec(`
			INSERT INTO task_channels (task_id, sort_order, channel, channel_ref)
			VALUES (?, ?, ?, ?)
		`,
			strings.TrimSpace(taskID),
			index,
			strings.TrimSpace(target.Channel),
			nullIfEmpty(strings.TrimSpace(target.ChannelRef)),
		); err != nil {
			return fmt.Errorf("insert task channel: %w", err)
		}
	}
	return nil
}

func replaceTaskChannelsTx(tx *sql.Tx, taskID string, targets []ChannelTarget) error {
	if _, err := tx.Exec(`DELETE FROM task_channels WHERE task_id = ?`, strings.TrimSpace(taskID)); err != nil {
		return fmt.Errorf("delete task channels: %w", err)
	}
	if err := insertTaskChannelsTx(tx, taskID, targets); err != nil {
		return err
	}
	return nil
}

func (r *Repository) hydrateTaskChannels(ctx context.Context, tasks []Task) error {
	if len(tasks) == 0 {
		return nil
	}

	indexByID := make(map[string]int, len(tasks))
	placeholders := make([]string, 0, len(tasks))
	args := make([]any, 0, len(tasks))
	for index, record := range tasks {
		indexByID[record.ID] = index
		placeholders = append(placeholders, "?")
		args = append(args, record.ID)
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT task_id, sort_order, channel, channel_ref
		FROM task_channels
		WHERE task_id IN (`+strings.Join(placeholders, ", ")+`)
		ORDER BY task_id ASC, sort_order ASC
	`, args...)
	if err != nil {
		return fmt.Errorf("query task channels: %w", err)
	}
	defer rows.Close()

	type taskChannelRow struct {
		taskID     string
		channel    string
		channelRef sql.NullString
	}

	channelsByTaskID := make(map[string][]ChannelTarget, len(tasks))
	for rows.Next() {
		var row taskChannelRow
		var sortOrder int
		if err := rows.Scan(&row.taskID, &sortOrder, &row.channel, &row.channelRef); err != nil {
			return fmt.Errorf("scan task channel: %w", err)
		}
		channelsByTaskID[row.taskID] = append(channelsByTaskID[row.taskID], ChannelTarget{
			Channel:    strings.TrimSpace(row.channel),
			ChannelRef: strings.TrimSpace(row.channelRef.String),
		})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate task channels: %w", err)
	}

	for taskID, index := range indexByID {
		effective := normalizeChannelTargets(channelsByTaskID[taskID])
		if len(effective) == 0 {
			effective = tasks[index].EffectiveChannels()
		}
		tasks[index].Channels = effective
		if len(effective) > 0 {
			tasks[index].Channel = effective[0].Channel
			tasks[index].ChannelRef = effective[0].ChannelRef
		}
	}
	return nil
}

func hydrateTaskChannelsTx(ctx context.Context, tx *sql.Tx, record *Task) error {
	if record == nil || strings.TrimSpace(record.ID) == "" {
		return nil
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT channel, channel_ref
		FROM task_channels
		WHERE task_id = ?
		ORDER BY sort_order ASC
	`, strings.TrimSpace(record.ID))
	if err != nil {
		return fmt.Errorf("query task channels: %w", err)
	}
	defer rows.Close()

	var targets []ChannelTarget
	for rows.Next() {
		var (
			channel string
			ref     sql.NullString
		)
		if err := rows.Scan(&channel, &ref); err != nil {
			return fmt.Errorf("scan task channel: %w", err)
		}
		targets = append(targets, ChannelTarget{
			Channel:    strings.TrimSpace(channel),
			ChannelRef: strings.TrimSpace(ref.String),
		})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate task channels: %w", err)
	}

	effective := normalizeChannelTargets(targets)
	if len(effective) == 0 {
		effective = record.EffectiveChannels()
	}
	record.Channels = effective
	if len(effective) > 0 {
		record.Channel = effective[0].Channel
		record.ChannelRef = effective[0].ChannelRef
	}
	return nil
}
