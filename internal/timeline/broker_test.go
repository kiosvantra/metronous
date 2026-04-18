package timeline

import (
	"testing"
	"time"
)

func TestBrokerUnsubscribeWaitsForConcurrentPublish(t *testing.T) {
	broker := NewBroker()
	conversationID := "conv-1"
	ch, unsubscribe := broker.Subscribe(conversationID, 1)
	t.Cleanup(unsubscribe)

	broker.mu.RLock()
	var sub *brokerSubscriber
	for candidate := range broker.subs[conversationID] {
		sub = candidate
		break
	}
	broker.mu.RUnlock()
	if sub == nil {
		t.Fatal("missing subscriber")
	}

	sub.mu.Lock()
	publishDone := make(chan struct{})
	go func() {
		defer close(publishDone)
		broker.Publish(TimelineEvent{ConversationID: conversationID})
	}()

	unsubscribeDone := make(chan struct{})
	go func() {
		defer close(unsubscribeDone)
		unsubscribe()
	}()

	select {
	case <-publishDone:
		t.Fatal("publish should wait while subscriber send path is locked")
	case <-time.After(20 * time.Millisecond):
	}
	select {
	case <-unsubscribeDone:
		t.Fatal("unsubscribe should wait while subscriber send path is locked")
	case <-time.After(20 * time.Millisecond):
	}

	sub.mu.Unlock()

	select {
	case <-publishDone:
	case <-time.After(time.Second):
		t.Fatal("publish did not complete")
	}
	select {
	case <-unsubscribeDone:
	case <-time.After(time.Second):
		t.Fatal("unsubscribe did not complete")
	}

	select {
	case _, ok := <-ch:
		if ok {
			select {
			case _, ok = <-ch:
				if ok {
					t.Fatal("subscriber channel should be closed after buffered event is drained")
				}
			default:
				t.Fatal("subscriber channel should be closed after unsubscribe")
			}
		}
	default:
		t.Fatal("subscriber channel should be closed or have a buffered event")
	}
}
