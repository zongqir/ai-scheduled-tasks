package channel

import "context"

type Message struct {
	Title    string
	Body     string
	Priority string
}

type SendResult struct {
	Provider string
	Detail   string
}

type Sender interface {
	Name() string
	Check(ctx context.Context) error
	Send(ctx context.Context, msg Message) (*SendResult, error)
}
