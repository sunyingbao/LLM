package streamout

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	redisv6 "code.byted.org/kv/redis-v6"
	redisstore "eino-cli/deepagent/coordinator/internal/infra/store/redis"
	"eino-cli/deepagent/coordinator/internal/service/eventlog"
	"github.com/bytedance/sonic"
)

func TestSubscribeStartsFromCurrentLiveSeq(t *testing.T) {
	ctx := context.Background()
	redisClient := newFakeRedis()
	svc := New(redisClient, &stubIDGen{ids: []int64{1001}}, WithConsumerTokenGenerator(func() string { return "consumer-1" }))

	if err := svc.FanoutEventRecords(ctx, "ns1", "sess-1", []eventlog.Event{{EventType: "old"}}); err != nil {
		t.Fatalf("fanout old event: %v", err)
	}
	meta, err := svc.OpenSubscription(ctx, "ns1", "sess-1", "")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	events, err := svc.PeekPendingEvents(ctx, meta.QueueID, meta.ConsumerToken, 10)
	if err != nil {
		t.Fatalf("peek after subscribe: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events after subscribe = %+v, want no replay of old live buffer", events)
	}
}

func TestRecoverReplaysPeekedButUnacknowledgedEvent(t *testing.T) {
	ctx := context.Background()
	redisClient := newFakeRedis()
	tokens := []string{"consumer-1", "consumer-2"}
	tokenIndex := 0
	svc := New(redisClient, &stubIDGen{ids: []int64{1001}}, WithConsumerTokenGenerator(func() string {
		token := tokens[tokenIndex]
		tokenIndex++
		return token
	}))

	meta, err := svc.OpenSubscription(ctx, "ns1", "sess-1", "")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := svc.FanoutEventRecords(ctx, "ns1", "sess-1", []eventlog.Event{{EventID: 41, EventType: "delta", Payload: []byte("one")}}); err != nil {
		t.Fatalf("fanout: %v", err)
	}
	peeked, err := svc.PeekPendingEvents(ctx, meta.QueueID, meta.ConsumerToken, 10)
	if err != nil {
		t.Fatalf("peek: %v", err)
	}
	if len(peeked) != 1 {
		t.Fatalf("peeked = %+v, want one event", peeked)
	}
	if err := svc.CloseQueue(ctx, meta.QueueID, meta.ConsumerToken); err != nil {
		t.Fatalf("close: %v", err)
	}

	recovered, err := svc.OpenSubscription(ctx, "ns1", "sess-1", meta.QueueID)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	replayed, err := svc.PeekPendingEvents(ctx, recovered.QueueID, recovered.ConsumerToken, 10)
	if err != nil {
		t.Fatalf("peek recovered: %v", err)
	}
	if len(replayed) != 1 || replayed[0].EventID != 41 || replayed[0].DeliveryID != peeked[0].DeliveryID {
		t.Fatalf("replayed = %+v, want unacknowledged event %+v", replayed, peeked[0])
	}
}

func TestRecoverContinuesAfterAcknowledgedSequence(t *testing.T) {
	ctx := context.Background()
	redisClient := newFakeRedis()
	tokens := []string{"consumer-1", "consumer-2"}
	tokenIndex := 0
	svc := New(redisClient, &stubIDGen{ids: []int64{1001}}, WithConsumerTokenGenerator(func() string {
		token := tokens[tokenIndex]
		tokenIndex++
		return token
	}))

	meta, err := svc.OpenSubscription(ctx, "ns1", "sess-1", "")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := svc.FanoutEventRecords(ctx, "ns1", "sess-1", []eventlog.Event{{EventID: 41, EventType: "first"}}); err != nil {
		t.Fatalf("fanout first: %v", err)
	}
	first, err := svc.PeekPendingEvents(ctx, meta.QueueID, meta.ConsumerToken, 10)
	if err != nil {
		t.Fatalf("peek first: %v", err)
	}
	if err := svc.AckDeliveredEvents(ctx, meta.QueueID, meta.ConsumerToken, first); err != nil {
		t.Fatalf("ack first: %v", err)
	}
	if err := svc.CloseQueue(ctx, meta.QueueID, meta.ConsumerToken); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := svc.FanoutEventRecords(ctx, "ns1", "sess-1", []eventlog.Event{{EventID: 42, EventType: "second"}}); err != nil {
		t.Fatalf("fanout second: %v", err)
	}

	recovered, err := svc.OpenSubscription(ctx, "ns1", "sess-1", meta.QueueID)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	pending, err := svc.PeekPendingEvents(ctx, recovered.QueueID, recovered.ConsumerToken, 10)
	if err != nil {
		t.Fatalf("peek recovered: %v", err)
	}
	if len(pending) != 1 || pending[0].EventID != 42 {
		t.Fatalf("pending = %+v, want only second event", pending)
	}

	var persisted redisstore.StreamQueueMeta
	if err := redisClient.StructGet(ctx, redisstore.StreamQueueMetaKey(meta.QueueID), &persisted); err != nil {
		t.Fatalf("load queue meta: %v", err)
	}
	if persisted.LastDeliveredSequence != 1 || persisted.LastDeliveredEventID != 41 {
		t.Fatalf("persisted delivery = %+v, want sequence=1 event_id=41", persisted)
	}
}

func TestFanoutAndPeekLiveEvents(t *testing.T) {
	ctx := context.Background()
	redisClient := newFakeRedis()
	svc := New(redisClient, &stubIDGen{ids: []int64{1001}}, WithConsumerTokenGenerator(func() string { return "consumer-1" }))

	meta, err := svc.OpenSubscription(ctx, "ns1", "sess-1", "")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	input := []eventlog.Event{
		{ThreadID: 10, EventType: "delta", Payload: []byte("a")},
		{ThreadID: 10, EventType: "delta", Payload: []byte("b")},
	}
	if err := svc.FanoutEventRecords(ctx, "ns1", "sess-1", input); err != nil {
		t.Fatalf("fanout: %v", err)
	}
	events, err := svc.PeekPendingEvents(ctx, meta.QueueID, meta.ConsumerToken, 10)
	if err != nil {
		t.Fatalf("peek: %v", err)
	}
	if len(events) != 2 || string(events[0].Payload) != "a" || string(events[1].Payload) != "b" {
		t.Fatalf("events = %+v, want ordered live events", events)
	}
	if events[0].SessionID != "sess-1" || !strings.HasPrefix(events[0].DeliveryID, liveDeliveryPrefix) {
		t.Fatalf("event metadata not filled: %+v", events[0])
	}
	again, err := svc.PeekPendingEvents(ctx, meta.QueueID, meta.ConsumerToken, 10)
	if err != nil {
		t.Fatalf("peek again: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second peek = %+v, want cursor advanced", again)
	}
}

func TestMultipleSubscribersShareSessionLiveBuffer(t *testing.T) {
	ctx := context.Background()
	redisClient := newFakeRedis()
	svc := New(redisClient, &stubIDGen{ids: []int64{1001, 1002}}, WithConsumerTokenGenerator(func() string { return "consumer" }))

	meta1, err := svc.OpenSubscription(ctx, "ns1", "sess-1", "")
	if err != nil {
		t.Fatalf("subscribe 1: %v", err)
	}
	meta2, err := svc.OpenSubscription(ctx, "ns1", "sess-1", "")
	if err != nil {
		t.Fatalf("subscribe 2: %v", err)
	}
	if err := svc.FanoutEventRecords(ctx, "ns1", "sess-1", []eventlog.Event{{EventType: "delta", Payload: []byte("x")}}); err != nil {
		t.Fatalf("fanout: %v", err)
	}
	events1, err := svc.PeekPendingEvents(ctx, meta1.QueueID, meta1.ConsumerToken, 10)
	if err != nil {
		t.Fatalf("peek 1: %v", err)
	}
	events2, err := svc.PeekPendingEvents(ctx, meta2.QueueID, meta2.ConsumerToken, 10)
	if err != nil {
		t.Fatalf("peek 2: %v", err)
	}
	if len(events1) != 1 || len(events2) != 1 || string(events1[0].Payload) != "x" || string(events2[0].Payload) != "x" {
		t.Fatalf("shared buffer events1=%+v events2=%+v, want both subscribers see event", events1, events2)
	}
	if redisClient.incrCalls[redisstore.SessionLiveSequenceKey("ns1", "sess-1")] != 1 {
		t.Fatalf("live seq incr calls = %+v, want one session write not per subscriber", redisClient.incrCalls)
	}
}

func TestPeekWaitsForTemporarilyMissingLiveEvent(t *testing.T) {
	ctx := context.Background()
	redisClient := newFakeRedis()
	svc := New(redisClient, &stubIDGen{ids: []int64{1001}}, WithConsumerTokenGenerator(func() string { return "consumer-1" }))

	meta, err := svc.OpenSubscription(ctx, "ns1", "sess-1", "")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	redisClient.seqs[redisstore.SessionLiveSequenceKey("ns1", "sess-1")] = 1
	events, err := svc.PeekPendingEvents(ctx, meta.QueueID, meta.ConsumerToken, 10)
	if err != nil {
		t.Fatalf("peek: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %+v, want no event while first live body is missing", events)
	}
	if err := redisClient.StructSet(ctx, redisstore.SessionLiveEventKey("ns1", "sess-1", 1), eventlog.Event{EventType: "delta", Payload: []byte("one")}); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	again, err := svc.PeekPendingEvents(ctx, meta.QueueID, meta.ConsumerToken, 10)
	if err != nil {
		t.Fatalf("peek again: %v", err)
	}
	if len(again) != 1 || string(again[0].Payload) != "one" {
		t.Fatalf("peek again = %+v, want delayed live event", again)
	}
}

func TestPeekSkipsMissingLiveEventAfterGrace(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(100, 0)
	redisClient := newFakeRedis()
	svc := New(
		redisClient,
		&stubIDGen{ids: []int64{1001}},
		WithClock(func() time.Time { return now }),
		WithConsumerTokenGenerator(func() string { return "consumer-1" }),
	)

	meta, err := svc.OpenSubscription(ctx, "ns1", "sess-1", "")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	redisClient.seqs[redisstore.SessionLiveSequenceKey("ns1", "sess-1")] = 2
	if err := redisClient.StructSet(ctx, redisstore.SessionLiveEventKey("ns1", "sess-1", 2), eventlog.Event{EventType: "delta", Payload: []byte("two")}); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	events, err := svc.PeekPendingEvents(ctx, meta.QueueID, meta.ConsumerToken, 10)
	if err != nil {
		t.Fatalf("peek: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %+v, want wait before missing live event grace expires", events)
	}

	now = now.Add(2*time.Second + time.Millisecond)
	afterGrace, err := svc.PeekPendingEvents(ctx, meta.QueueID, meta.ConsumerToken, 10)
	if err != nil {
		t.Fatalf("peek after grace: %v", err)
	}
	if len(afterGrace) != 1 || string(afterGrace[0].Payload) != "two" {
		t.Fatalf("after grace = %+v, want skip missing seq and deliver next event", afterGrace)
	}
	final, err := svc.PeekPendingEvents(ctx, meta.QueueID, meta.ConsumerToken, 10)
	if err != nil {
		t.Fatalf("final peek: %v", err)
	}
	if len(final) != 0 {
		t.Fatalf("final peek = %+v, want cursor advanced after grace skip", final)
	}
}

type fakeRedis struct {
	mu        sync.Mutex
	kv        map[string]string
	zsets     map[string]map[string]float64
	seqs      map[string]int64
	incrCalls map[string]int
}

func newFakeRedis() *fakeRedis {
	return &fakeRedis{
		kv:        map[string]string{},
		zsets:     map[string]map[string]float64{},
		seqs:      map[string]int64{},
		incrCalls: map[string]int{},
	}
}

func (f *fakeRedis) StructGet(_ context.Context, key string, v interface{}) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	raw, ok := f.kv[key]
	if !ok {
		return redisv6.Nil
	}
	return sonic.UnmarshalString(raw, v)
}

func (f *fakeRedis) StructGetPrimary(ctx context.Context, key string, v interface{}) error {
	return f.StructGet(ctx, key, v)
}

func (f *fakeRedis) StructMGet(_ context.Context, keys []string, values interface{}) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	raws := make([]string, 0, len(keys))
	for _, key := range keys {
		raw, ok := f.kv[key]
		if !ok {
			raws = append(raws, "null")
			continue
		}
		raws = append(raws, raw)
	}
	return sonic.UnmarshalString("["+strings.Join(raws, ",")+"]", values)
}

func (f *fakeRedis) StructMGetPrimary(ctx context.Context, keys []string, values interface{}) error {
	return f.StructMGet(ctx, keys, values)
}

func (f *fakeRedis) StructSet(_ context.Context, key string, v interface{}) error {
	raw, err := sonic.Marshal(v)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kv[key] = string(raw)
	return nil
}

func (f *fakeRedis) StructSetTTL(ctx context.Context, key string, v interface{}, _ time.Duration) error {
	return f.StructSet(ctx, key, v)
}

func (f *fakeRedis) StructMSet(ctx context.Context, keys []string, values []interface{}) error {
	if len(keys) != len(values) {
		return fmt.Errorf("length mismatch")
	}
	for idx := range keys {
		if err := f.StructSet(ctx, keys[idx], values[idx]); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeRedis) GetInt64Primary(_ context.Context, key string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seqs[key], nil
}

func (f *fakeRedis) Incr(_ context.Context, key string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seqs[key]++
	f.incrCalls[key]++
	return f.seqs[key], nil
}

func (f *fakeRedis) ZAdd(_ context.Context, key string, score float64, member string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.zsets[key]; !ok {
		f.zsets[key] = map[string]float64{}
	}
	f.zsets[key][member] = score
	return nil
}

func (f *fakeRedis) ZRange(_ context.Context, key string, start int64, stop int64) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pairs := sortPairs(f.zsets[key])
	if len(pairs) == 0 {
		return nil, nil
	}
	if start < 0 {
		start = 0
	}
	if stop < 0 || stop >= int64(len(pairs)) {
		stop = int64(len(pairs) - 1)
	}
	if start > stop || start >= int64(len(pairs)) {
		return nil, nil
	}
	result := make([]string, 0, stop-start+1)
	for idx := start; idx <= stop; idx++ {
		result = append(result, pairs[idx].member)
	}
	return result, nil
}

func (f *fakeRedis) ZRangePrimary(ctx context.Context, key string, start int64, stop int64) ([]string, error) {
	return f.ZRange(ctx, key, start, stop)
}

func (f *fakeRedis) ZRangeByScore(_ context.Context, key string, rangeOpt redisv6.ZRangeBy) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	min, _ := strconv.ParseFloat(rangeOpt.Min, 64)
	pairs := sortPairs(f.zsets[key])
	result := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		if pair.score >= min {
			result = append(result, pair.member)
		}
	}
	return result, nil
}

func (f *fakeRedis) ZRangeByScorePrimary(ctx context.Context, key string, rangeOpt redisv6.ZRangeBy) ([]string, error) {
	return f.ZRangeByScore(ctx, key, rangeOpt)
}

func (f *fakeRedis) ZCard(_ context.Context, key string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return int64(len(f.zsets[key])), nil
}

func (f *fakeRedis) ZCardPrimary(ctx context.Context, key string) (int64, error) {
	return f.ZCard(ctx, key)
}

func (f *fakeRedis) ZScore(_ context.Context, key string, member string) (float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	score, ok := f.zsets[key][member]
	if !ok {
		return 0, redisv6.Nil
	}
	return score, nil
}

func (f *fakeRedis) ZScorePrimary(ctx context.Context, key string, member string) (float64, error) {
	return f.ZScore(ctx, key, member)
}

func (f *fakeRedis) ZRem(_ context.Context, key string, members []interface{}) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var removed int64
	for _, member := range members {
		memberStr := fmt.Sprint(member)
		if _, ok := f.zsets[key][memberStr]; ok {
			delete(f.zsets[key], memberStr)
			removed++
		}
	}
	return removed, nil
}

func (f *fakeRedis) Del(_ context.Context, keys ...string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var removed int64
	for _, key := range keys {
		if _, ok := f.kv[key]; ok {
			delete(f.kv, key)
			removed++
		}
	}
	return removed, nil
}

type scorePair struct {
	member string
	score  float64
}

func sortPairs(values map[string]float64) []scorePair {
	pairs := make([]scorePair, 0, len(values))
	for member, score := range values {
		pairs = append(pairs, scorePair{member: member, score: score})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].score == pairs[j].score {
			return pairs[i].member < pairs[j].member
		}
		return pairs[i].score < pairs[j].score
	})
	return pairs
}

type stubIDGen struct {
	ids []int64
	idx int
}

func (s *stubIDGen) NextID(context.Context) (int64, error) {
	if s.idx >= len(s.ids) {
		return 0, nil
	}
	value := s.ids[s.idx]
	s.idx++
	return value, nil
}

var _ redisstore.Client = (*fakeRedis)(nil)
