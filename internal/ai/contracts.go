package ai

type CreateTaskResponse struct {
	Action               string    `json:"action"`
	RequiresConfirmation bool      `json:"requires_confirmation"`
	Question             string    `json:"question,omitempty"`
	Task                 TaskDraft `json:"task,omitempty"`
	DraftTask            TaskDraft `json:"draft_task,omitempty"`
}

type TaskDraft struct {
	RawInput     string   `json:"raw_input"`
	Summary      string   `json:"summary"`
	Action       string   `json:"action,omitempty"`
	Agent        string   `json:"agent,omitempty"`
	Instruction  string   `json:"instruction,omitempty"`
	Model        string   `json:"model,omitempty"`
	ScheduleType string   `json:"schedule_type"`
	Timezone     string   `json:"timezone"`
	RepeatRule   string   `json:"repeat_rule,omitempty"`
	TimeOfDay    string   `json:"time_of_day,omitempty"`
	NextRunAt    string   `json:"next_run_at"`
	CWD          string   `json:"cwd"`
	Channel      string   `json:"channel"`
	Tags         []string `json:"tags,omitempty"`
}
