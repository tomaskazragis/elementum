package broadcast

import "github.com/anacrolix/missinggo/pubsub"

// Closer ...
type Closer struct {
	*pubsub.PubSub
	done bool
}

// NewCloser ...
func NewCloser() *Closer {
	return &Closer{pubsub.NewPubSub(), false}
}

// Set ...
func (c *Closer) Set() {
	c.done = true
	c.Publish(struct{}{})
}

// IsSet ...
func (c *Closer) IsSet() bool {
	return c.done
}

// Listen ...
func (c *Closer) Listen() <-chan interface{} {
	return c.Subscribe().Values
}
