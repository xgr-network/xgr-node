package network

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"
	testproto "github.com/xgr-network/xgr-node/network/proto"
)

func NumSubscribers(srv *Server, topic string) int {
	return len(srv.ps.ListPeers(topic))
}

func WaitForSubscribers(ctx context.Context, srv *Server, topic string, expectedNumPeers int) error {
	for {
		if n := NumSubscribers(srv, topic); n >= expectedNumPeers {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("canceled")
		case <-time.After(100 * time.Millisecond):
			continue
		}
	}
}

func TestSimpleGossip(t *testing.T) {
	numServers := 9
	sentMessage := fmt.Sprintf("%d", time.Now().UTC().Unix())

	servers, createErr := createServers(numServers, nil)
	require.NoError(t, createErr, "Unable to create servers")

	var (
		receivedMu  sync.Mutex
		receivedBy  = make(map[int]struct{}, numServers)
		callbackErr error
	)

	t.Cleanup(func() {
		closeTestServers(t, servers)
	})

	joinErrors := MeshJoin(servers...)
	require.Empty(t, joinErrors, "Unable to join servers [%d], %v", len(joinErrors), joinErrors)

	topicName := "msg-pub-sub"
	serverTopics := make([]*Topic, numServers)

	for i := 0; i < numServers; i++ {
		topic, topicErr := servers[i].NewTopic(topicName, &testproto.GenericMessage{})
		require.NoError(t, topicErr, "Unable to create topic")

		serverTopics[i] = topic

		serverIndex := i
		subscribeErr := topic.Subscribe(func(obj interface{}, _ peer.ID) {
			genericMessage, ok := obj.(*testproto.GenericMessage)
			receivedMu.Lock()
			defer receivedMu.Unlock()

			if !ok {
				if callbackErr == nil {
					callbackErr = fmt.Errorf("invalid type assert for server %d", serverIndex)
				}

				return
			}

			if genericMessage.Message == sentMessage {
				receivedBy[serverIndex] = struct{}{}
			}
		})
		require.NoError(t, subscribeErr, "Unable to subscribe to topic")
	}

	publisherTopic := serverTopics[0]

	require.Eventually(t, func() bool {
		for i := range servers {
			if NumSubscribers(servers[i], topicName) < len(servers)-1 {
				return false
			}
		}

		return true
	}, 10*time.Second, 100*time.Millisecond, "Unable to wait for subscribers on all servers")

	err := publisherTopic.Publish(
		&testproto.GenericMessage{
			Message: sentMessage,
		})
	require.NoError(t, err, "Unable to publish message")

	require.Eventually(t, func() bool {
		receivedMu.Lock()
		defer receivedMu.Unlock()

		return callbackErr != nil || len(receivedBy) == len(servers)
	}, 15*time.Second, 100*time.Millisecond, "Multicast messages not received before timeout")

	receivedMu.Lock()
	defer receivedMu.Unlock()
	require.NoError(t, callbackErr)
	require.Len(t, receivedBy, len(servers), "Expected all servers to receive the gossip message")
}

func Test_RepeatedClose(t *testing.T) {
	topic := &Topic{
		closeCh: make(chan struct{}),
	}

	// Call Close() twice to ensure that underlying logic (e.g. channel close) is
	// only executed once.
	topic.Close()
	topic.Close()
}
