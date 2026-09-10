package mail

import "context"

type Sender interface {
	Send(context.Context, Message) (Result, error)
}
type Message struct {
	ID, From, To, Subject, HTML, Text, UnsubscribeURL string
	Attachments                                       []Attachment
}
type Attachment struct {
	Name, ContentType string
	Data              []byte
}
type Result struct{ State, Detail string }

const (
	StateAccepted  = "accepted"
	StateTransient = "transient"
	StatePermanent = "permanent"
	StateUncertain = "uncertain"
)
