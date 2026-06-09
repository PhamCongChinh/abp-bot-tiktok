package kafka

import (
	"context"
	"time"

	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"
)

type Consumer struct {
	reader *kafka.Reader
	log    *zap.Logger
}

func NewConsumer(brokers []string, topic, groupID string, log *zap.Logger) *Consumer {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:        brokers,
		Topic:          topic,
		GroupID:        groupID,
		MinBytes:       1,
		MaxBytes:       10e6,
		CommitInterval: time.Second,
	})
	return &Consumer{reader: r, log: log}
}

// Consume blocks until ctx is cancelled, calling handler for each message.
func (c *Consumer) Consume(ctx context.Context, handler func([]byte) error) {
	for {
		m, err := c.reader.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			c.log.Error("kafka read error", zap.Error(err))
			time.Sleep(2 * time.Second)
			continue
		}
		c.log.Sugar().Infof("[kafka] offset=%d partition=%d len=%d", m.Offset, m.Partition, len(m.Value))
		if err := handler(m.Value); err != nil {
			c.log.Error("[kafka] handler error", zap.Error(err))
		}
	}
}

func (c *Consumer) Close() error {
	return c.reader.Close()
}
