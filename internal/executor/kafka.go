package executor

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl"
	"github.com/segmentio/kafka-go/sasl/plain"
	"github.com/segmentio/kafka-go/sasl/scram"

	"github.com/testplane/testplane/internal/assert"
	"github.com/testplane/testplane/internal/auth"
	"github.com/testplane/testplane/internal/model"
)

// KafkaSession remembers where each topic ended when a scenario started, so
// a consume step placed after the call that emits an event still sees it.
type KafkaSession struct {
	mu      sync.Mutex
	offsets map[string]map[int]int64
}

func NewKafkaSession() *KafkaSession { return &KafkaSession{offsets: map[string]map[int]int64{}} }

func brokers(s *model.KafkaStep) []string {
	var out []string
	for _, b := range strings.Split(s.Brokers, ",") {
		if b = strings.TrimSpace(b); b != "" {
			out = append(out, b)
		}
	}
	return out
}

func kafkaSecurity(s *model.KafkaStep, creds *auth.Credentials) (*tls.Config, sasl.Mechanism, error) {
	var tc *tls.Config
	if creds.TLS != nil {
		tc = creds.TLS.Clone()
	} else if s.TLS {
		tc = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	var mech sasl.Mechanism
	var err error
	switch strings.ToLower(s.SASL) {
	case "":
	case "plain":
		mech = plain.Mechanism{Username: creds.Username, Password: creds.Password}
	case "scram-sha-256":
		mech, err = scram.Mechanism(scram.SHA256, creds.Username, creds.Password)
	case "scram-sha-512":
		mech, err = scram.Mechanism(scram.SHA512, creds.Username, creds.Password)
	default:
		err = fmt.Errorf("unknown sasl mechanism %q", s.SASL)
	}
	if mech != nil && creds.Username == "" {
		err = errors.New("kafka sasl needs a basic auth profile for the username and password")
	}
	return tc, mech, err
}

func kafkaDialer(s *model.KafkaStep, creds *auth.Credentials) (*kafka.Dialer, error) {
	tc, mech, err := kafkaSecurity(s, creds)
	if err != nil {
		return nil, err
	}
	return &kafka.Dialer{Timeout: 10 * time.Second, DualStack: true, TLS: tc, SASLMechanism: mech}, nil
}

// partitionOffsets returns the first or last offset of every partition.
func partitionOffsets(ctx context.Context, d *kafka.Dialer, s *model.KafkaStep, last bool) (map[int]int64, error) {
	bs := brokers(s)
	if len(bs) == 0 {
		return nil, errors.New("kafka.brokers is empty")
	}
	conn, err := d.DialContext(ctx, "tcp", bs[0])
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", bs[0], err)
	}
	parts, err := conn.ReadPartitions(s.Topic)
	conn.Close()
	if err != nil {
		return nil, fmt.Errorf("read partitions of %s: %w", s.Topic, err)
	}
	out := map[int]int64{}
	for _, p := range parts {
		lc, err := d.DialLeader(ctx, "tcp", bs[0], s.Topic, p.ID)
		if err != nil {
			return nil, fmt.Errorf("connect to leader of %s/%d: %w", s.Topic, p.ID, err)
		}
		var off int64
		if last {
			off, err = lc.ReadLastOffset()
		} else {
			off, err = lc.ReadFirstOffset()
		}
		lc.Close()
		if err != nil {
			return nil, fmt.Errorf("read offset of %s/%d: %w", s.Topic, p.ID, err)
		}
		out[p.ID] = off
	}
	return out, nil
}

func sessionKey(s *model.KafkaStep) string { return s.Brokers + "|" + s.Topic }

// Mark records the current end of a topic. The engine calls it for every
// consume step before the scenario's first test runs.
func (k *KafkaSession) Mark(ctx context.Context, s *model.KafkaStep, creds *auth.Credentials) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if _, ok := k.offsets[sessionKey(s)]; ok {
		return nil
	}
	d, err := kafkaDialer(s, creds)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	offs, err := partitionOffsets(ctx, d, s, true)
	if err != nil {
		return err
	}
	k.offsets[sessionKey(s)] = offs
	return nil
}

// Kafka produces a message or waits for one that satisfies step.Match.
func Kafka(ctx context.Context, s *model.KafkaStep, creds *auth.Credentials, sess *KafkaSession) (*model.Response, error) {
	if strings.EqualFold(s.Action, "produce") {
		return kafkaProduce(ctx, s, creds)
	}
	if s.Action != "" && !strings.EqualFold(s.Action, "consume") {
		return nil, fmt.Errorf("kafka.action must be consume or produce, not %q", s.Action)
	}
	return kafkaConsume(ctx, s, creds, sess)
}

func kafkaProduce(ctx context.Context, s *model.KafkaStep, creds *auth.Credentials) (*model.Response, error) {
	tc, mech, err := kafkaSecurity(s, creds)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout(s.TimeoutMs, 15000))
	defer cancel()
	w := &kafka.Writer{
		Addr:         kafka.TCP(brokers(s)...),
		Topic:        s.Topic,
		Balancer:     &kafka.Hash{},
		RequiredAcks: kafka.RequireAll,
		BatchTimeout: 10 * time.Millisecond,
		Transport:    &kafka.Transport{TLS: tc, SASL: mech},
	}
	defer w.Close()
	var value []byte
	switch v := s.Value.(type) {
	case nil:
	case string:
		value = []byte(v)
	default:
		value = []byte(mustJSON(v))
	}
	msg := kafka.Message{Value: value}
	if s.Key != "" {
		msg.Key = []byte(s.Key)
	}
	for k, v := range s.Headers {
		msg.Headers = append(msg.Headers, kafka.Header{Key: k, Value: []byte(v)})
	}
	start := time.Now()
	if err := w.WriteMessages(ctx, msg); err != nil {
		return nil, fmt.Errorf("produce to %s: %w", s.Topic, err)
	}
	doc := mustJSON(map[string]any{"produced": true, "topic": s.Topic})
	return &model.Response{StatusText: "PRODUCED", Body: doc, JSON: doc, DurationMs: time.Since(start).Milliseconds()}, nil
}

func kafkaConsume(ctx context.Context, s *model.KafkaStep, creds *auth.Credentials, sess *KafkaSession) (*model.Response, error) {
	d, err := kafkaDialer(s, creds)
	if err != nil {
		return nil, err
	}
	wait := timeout(s.TimeoutMs, 10000)
	ctx, cancel := context.WithTimeout(ctx, wait+15*time.Second)
	defer cancel()

	var offs map[int]int64
	switch strings.ToLower(s.From) {
	case "earliest":
		offs, err = partitionOffsets(ctx, d, s, false)
	case "", "scenario_start":
		if sess != nil {
			sess.mu.Lock()
			offs = sess.offsets[sessionKey(s)]
			sess.mu.Unlock()
		}
		if offs == nil {
			offs, err = partitionOffsets(ctx, d, s, true)
		}
	default:
		err = fmt.Errorf("kafka.from must be scenario_start or earliest, not %q", s.From)
	}
	if err != nil {
		return nil, err
	}

	readCtx, stop := context.WithTimeout(ctx, wait)
	defer stop()
	found := make(chan string, 1)
	var scanned int64
	var mu sync.Mutex
	var wg sync.WaitGroup
	start := time.Now()
	for part, off := range offs {
		r := kafka.NewReader(kafka.ReaderConfig{
			Brokers: brokers(s), Topic: s.Topic, Partition: part, Dialer: d,
			MinBytes: 1, MaxBytes: 10 << 20, MaxWait: 250 * time.Millisecond,
		})
		if err := r.SetOffset(off); err != nil {
			r.Close()
			return nil, fmt.Errorf("seek %s/%d: %w", s.Topic, part, err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer r.Close()
			for {
				m, err := r.ReadMessage(readCtx)
				if err != nil {
					return
				}
				mu.Lock()
				scanned++
				mu.Unlock()
				doc := messageDoc(m)
				if matches(doc, s.Match) {
					select {
					case found <- doc:
					default:
					}
					stop()
					return
				}
			}
		}()
	}
	wg.Wait()
	resp := &model.Response{DurationMs: time.Since(start).Milliseconds()}
	select {
	case doc := <-found:
		resp.Status, resp.StatusText, resp.Body, resp.JSON = 1, "FOUND", doc, doc
	default:
		doc := mustJSON(map[string]any{"found": false, "scanned": scanned, "topic": s.Topic})
		resp.StatusText, resp.Body, resp.JSON = "NOT_FOUND", doc, doc
	}
	return resp, nil
}

func messageDoc(m kafka.Message) string {
	headers := map[string]string{}
	for _, h := range m.Headers {
		headers[h.Key] = string(h.Value)
	}
	var value any = string(m.Value)
	if json.Valid(m.Value) {
		value = json.RawMessage(m.Value)
	}
	return mustJSON(map[string]any{
		"found": true, "topic": m.Topic, "partition": m.Partition, "offset": m.Offset,
		"key": string(m.Key), "value": value, "headers": headers, "time": m.Time.UTC().Format(time.RFC3339Nano),
	})
}

func matches(doc string, checks []model.Assertion) bool {
	for _, a := range checks {
		if !assert.Eval(doc, a).Passed {
			return false
		}
	}
	return true
}
