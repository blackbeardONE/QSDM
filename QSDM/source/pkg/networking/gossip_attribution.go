package networking

import (
	pubsub "github.com/libp2p/go-libp2p-pubsub"
)

// gossipAttribution returns the identity a gossip message's CONTENT is
// attributed to, and whether the read loop should skip the message as our own.
//
// 2026-11-14: the relay read loops used msg.ReceivedFrom, the direct
// mesh neighbour that forwarded the message, as the identity for every content
// penalty (BFT equivocation, invalid transaction, invalid evidence, rate limit).
// GossipSub forwards any message that passes the transport guard, so a peer
// could author penalty-triggering content and let an honest forwarder take the
// blame: three forwarded equivocations banned (and disconnected) the forwarder.
//
// Under StrictSign (DefaultPubsubSignaturePolicy) msg.GetFrom() is the
// authenticated author, so content is attributed to it. The forwarder is only
// responsible for transport, which libp2p and the connection gater handle.
// ReceivedFrom is the fallback only for a message without an author field,
// which StrictSign rejects before delivery.
func gossipAttribution(msg *pubsub.Message, selfID string) (id string, skip bool) {
	if msg == nil {
		return "", true
	}
	fwd := msg.ReceivedFrom.String()
	author := ""
	if from := msg.GetFrom(); from != "" {
		author = from.String()
	}
	if fwd == selfID || (author != "" && author == selfID) {
		return "", true
	}
	if author != "" {
		return author, false
	}
	return fwd, false
}
