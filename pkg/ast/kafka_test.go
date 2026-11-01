package ast

import "testing"

const kafkaTopicSrc = `package sample

import (
	"context"

	"github.com/segmentio/kafka-go"
)

func NewConsumer() *kafka.Reader {
	return kafka.NewReader(kafka.ReaderConfig{
		Brokers: []string{"localhost:9092"},
		Topic:   "payment.succeeded",
		GroupID: "order-service",
	})
}

func (c *Consumer) Consume(ctx context.Context) error {
	for {
		msg, err := c.reader.FetchMessage(ctx)
		if err != nil {
			return err
		}
		_ = msg
	}
}
`

func TestExtractKafkaTopic(t *testing.T) {
	path, tree, code := parseSample(t, kafkaTopicSrc)
	defer tree.Close()

	qs := mustQuerySet(t, []string{"go/kafka.scm"})
	defer qs.Close()

	res := qs.Extract(path, tree, code)
	if len(res.Entries) != 1 {
		t.Fatalf("want 1 entry, got %+v", res.Entries)
	}
	if res.Entries[0].Kind != EntryKafkaConsumer {
		t.Fatalf("kind = %s, want KafkaConsumer", res.Entries[0].Kind)
	}
	if res.Entries[0].Topic != "payment.succeeded" {
		t.Errorf("topic = %q, want payment.succeeded", res.Entries[0].Topic)
	}
}
