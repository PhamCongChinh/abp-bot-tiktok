package kafka

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"
)

type Producer struct {
	writer *kafka.Writer
	log    *zap.Logger
}

func NewProducer(brokers []string, topic string, log *zap.Logger) *Producer {
	w := &kafka.Writer{
		Addr:     kafka.TCP(brokers...),
		Topic:    topic,
		Balancer: &kafka.LeastBytes{},
	}
	return &Producer{writer: w, log: log}
}

func (p *Producer) Publish(ctx context.Context, key string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	msg := kafka.Message{Value: data}
	if key != "" {
		msg.Key = []byte(key)
	}
	if err := p.writer.WriteMessages(ctx, msg); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	p.log.Sugar().Infof("[kafka] produced key=%s len=%d topic=%s", key, len(data), p.writer.Topic)
	return nil
}

func (p *Producer) Close() error {
	return p.writer.Close()
}
