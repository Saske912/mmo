package natsbus

import (
	"errors"
	"time"

	"github.com/nats-io/nats.go"
)

var ErrEmptyURL = errors.New("empty NATS server URL")

// Client тонкая обёртка над NATS core (без JetStream на этом этапе).
type Client struct {
	conn *nats.Conn
}

// Connect устанавливает соединение (NATS_URL или собранный URL из config).
func Connect(url string, opts ...nats.Option) (*Client, error) {
	if url == "" {
		return nil, ErrEmptyURL
	}
	conn, err := nats.Connect(url, opts...)
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn}, nil
}

func (c *Client) Publish(subject string, data []byte) error {
	return c.conn.Publish(subject, data)
}

func (c *Client) Subscribe(subject string, cb nats.MsgHandler) (*nats.Subscription, error) {
	return c.conn.Subscribe(subject, cb)
}

// Flush сбрасывает исходящий буфер.
func (c *Client) Flush() error {
	return c.conn.Flush()
}

// FlushTimeout — flush с таймаутом.
func (c *Client) FlushTimeout(t time.Duration) error {
	return c.conn.FlushTimeout(t)
}

func (c *Client) Close() {
	c.conn.Close()
}

func (c *Client) Conn() *nats.Conn {
	return c.conn
}
