package ibft

import (
	"github.com/0xPolygon/go-ibft/messages/proto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/xgr-network/xgr-node/network"
)

type transport interface {
	Multicast(msg *proto.IbftMessage) error
}

type gossipTransport struct {
	topic *network.Topic
}

func (g *gossipTransport) Multicast(msg *proto.IbftMessage) error {
	return g.topic.Publish(msg)
}

func (i *backendIBFT) Multicast(msg *proto.IbftMessage) {
	if err := i.transport.Multicast(msg); err != nil {
		i.logger.Error("fail to gossip", "err", err)
	}

	// Ensure locally-originated messages are visible to the consensus core immediately.
	// Relying solely on pubsub loopback can cause higher-round RCC starvation in some
	// network setups where self-published messages aren't delivered back to the sender.
	if i.consensus != nil && i.isActiveValidator() && msg != nil {
		i.consensus.AddMessage(msg)
	}
}

// setupTransport sets up the gossip transport protocol
func (i *backendIBFT) setupTransport() error {
	// Define a new topic
	topic, err := i.network.NewTopic(ibftProto, &proto.IbftMessage{})
	if err != nil {
		return err
	}

	// Subscribe to the newly created topic
	if err := topic.Subscribe(
		func(obj interface{}, _ peer.ID) {
			if !i.isActiveValidator() {
				return
			}

			msg, ok := obj.(*proto.IbftMessage)
			if !ok {
				i.logger.Error("invalid type assertion for message request")

				return
			}

			i.consensus.AddMessage(msg)
		},
	); err != nil {
		return err
	}

	i.transport = &gossipTransport{topic: topic}

	return nil
}
