package natsbus

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
)

var ErrEmptyURL = errors.New("empty NATS server URL")

// Client тонкая обёртка над NATS core (без JetStream на этом этапе).
type Client struct {
	conn            *nats.Conn
	reconnectCount  atomic.Int64
	disconnectCount atomic.Int64
}

// JetStreamConfig задаёт минимальные параметры bootstrap stream.
type JetStreamConfig struct {
	StreamName string
	Subjects   []string
	MaxAge     time.Duration
	Replicas   int
}

// DefaultJetStreamConfig покрывает базовые subjects из Phase 0.1.
func DefaultJetStreamConfig() JetStreamConfig {
	return JetStreamConfig{
		StreamName: "MMO_EVENTS",
		Subjects: []string{
			SubjectCellEvents,
			SubjectCellMigration + ".*",
			SubjectGridCommands,
		},
		MaxAge:   24 * time.Hour,
		Replicas: 1,
	}
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

// ReconnectConfig базовые параметры reconnect-политики для подписчиков.
type ReconnectConfig struct {
	RetryOnFailedConnect bool
	MaxReconnects        int
	ReconnectWait        time.Duration
	ConnectTimeout       time.Duration
}

// DefaultReconnectConfig безопасные дефолты для долгоживущих подписчиков.
func DefaultReconnectConfig() ReconnectConfig {
	return ReconnectConfig{
		RetryOnFailedConnect: true,
		MaxReconnects:        -1, // бесконечно
		ReconnectWait:        2 * time.Second,
		ConnectTimeout:       2 * time.Second,
	}
}

// ConnectResilient подключает NATS с reconnect-политикой для долгоживущих sub.
// Дополнительные opts могут переопределить дефолтные параметры.
func ConnectResilient(url string, cfg ReconnectConfig, opts ...nats.Option) (*Client, error) {
	if url == "" {
		return nil, ErrEmptyURL
	}
	c := &Client{}
	base := []nats.Option{
		nats.RetryOnFailedConnect(cfg.RetryOnFailedConnect),
		nats.MaxReconnects(cfg.MaxReconnects),
		nats.ReconnectWait(cfg.ReconnectWait),
		nats.Timeout(cfg.ConnectTimeout),
		nats.DisconnectErrHandler(func(_ *nats.Conn, _ error) {
			c.disconnectCount.Add(1)
		}),
		nats.ReconnectHandler(func(_ *nats.Conn) {
			c.reconnectCount.Add(1)
		}),
	}
	conn, err := nats.Connect(url, append(base, opts...)...)
	if err != nil {
		return nil, err
	}
	c.conn = conn
	return c, nil
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

// JetStream возвращает контекст JetStream поверх текущего подключения.
func (c *Client) JetStream() (nats.JetStreamContext, error) {
	return c.conn.JetStream()
}

// EnsureStream создаёт stream, если он отсутствует.
func EnsureStream(ctx context.Context, js nats.JetStreamContext, cfg JetStreamConfig) (*nats.StreamInfo, error) {
	if strings.TrimSpace(cfg.StreamName) == "" {
		return nil, fmt.Errorf("jetstream stream name is empty")
	}
	if len(cfg.Subjects) == 0 {
		return nil, fmt.Errorf("jetstream subjects are empty")
	}
	if cfg.MaxAge <= 0 {
		cfg.MaxAge = 24 * time.Hour
	}
	if cfg.Replicas <= 0 {
		cfg.Replicas = 1
	}

	if si, err := js.StreamInfo(cfg.StreamName, nats.Context(ctx)); err == nil {
		return si, nil
	}
	return js.AddStream(&nats.StreamConfig{
		Name:      cfg.StreamName,
		Subjects:  cfg.Subjects,
		Storage:   nats.FileStorage,
		Retention: nats.LimitsPolicy,
		MaxAge:    cfg.MaxAge,
		Replicas:  cfg.Replicas,
	}, nats.Context(ctx))
}

// ReconnectStats текстовый статус для смоук/диагностики.
func (c *Client) ReconnectStats() string {
	return fmt.Sprintf("disconnects=%d reconnects=%d", c.disconnectCount.Load(), c.reconnectCount.Load())
}
