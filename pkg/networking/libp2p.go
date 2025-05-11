package networking

import (
    "context"
    "fmt"
    "sync"

    "github.com/blackbeardONE/QSDM/internal/logging"
    libp2p "github.com/libp2p/go-libp2p"
    "github.com/libp2p/go-libp2p/core/host"
    "github.com/libp2p/go-libp2p/core/peer"
    "github.com/libp2p/go-libp2p/p2p/discovery/mdns"
    pubsub "github.com/libp2p/go-libp2p-pubsub"
)

type DiscoveryNotifee struct {
    h host.Host
}

func (n *DiscoveryNotifee) HandlePeerFound(pi peer.AddrInfo) {
    logging.Info.Printf("Discovered new peer: %s\n", pi.ID.String())
    if err := n.h.Connect(context.Background(), pi); err != nil {
        logging.Error.Printf("Failed to connect to peer %s: %s\n", pi.ID.String(), err)
    }
}

type Network struct {
    Host       host.Host
    PubSub     *pubsub.PubSub
    Topic      *pubsub.Topic
    Sub        *pubsub.Subscription
    ctx        context.Context
    cancel     context.CancelFunc
    msgHandler func(msg []byte)
    mu         sync.Mutex
}

func SetupLibP2P(ctx context.Context) (*Network, error) {
    h, err := libp2p.New()
    if err != nil {
        return nil, err
    }

    // Setup mDNS discovery to find local peers
    notifee := &DiscoveryNotifee{h: h}
    mdnsService := mdns.NewMdnsService(h, "qsmd-mdns", notifee)
    if mdnsService == nil {
        return nil, fmt.Errorf("failed to start mDNS service")
    }

    ps, err := pubsub.NewGossipSub(ctx, h)
    if err != nil {
        return nil, fmt.Errorf("failed to create pubsub: %w", err)
    }

    topic, err := ps.Join("qsmd-transactions")
    if err != nil {
        return nil, fmt.Errorf("failed to join pubsub topic: %w", err)
    }

    sub, err := topic.Subscribe()
    if err != nil {
        return nil, fmt.Errorf("failed to subscribe to pubsub topic: %w", err)
    }

    networkCtx, cancel := context.WithCancel(ctx)

    net := &Network{
        Host:   h,
        PubSub: ps,
        Topic:  topic,
        Sub:    sub,
        ctx:    networkCtx,
        cancel: cancel,
    }

    go net.handleMessages()

    logging.Info.Printf("LibP2P host created. ID: %s\n", h.ID().String())
    return net, nil
}

func (n *Network) handleMessages() {
    for {
        msg, err := n.Sub.Next(n.ctx)
        if err != nil {
            if n.ctx.Err() != nil {
                return
            }
            logging.Error.Printf("Error reading pubsub message: %s\n", err)
            continue
        }
        if msg.ReceivedFrom == n.Host.ID() {
            continue // Ignore messages from self
        }
        n.mu.Lock()
        if n.msgHandler != nil {
            n.msgHandler(msg.Data)
        }
        n.mu.Unlock()
    }
}

func (n *Network) SetMessageHandler(handler func(msg []byte)) {
    n.mu.Lock()
    defer n.mu.Unlock()
    n.msgHandler = handler
}

func (n *Network) Broadcast(msg []byte) error {
    return n.Topic.Publish(n.ctx, msg)
}

func (n *Network) Close() error {
    n.cancel()
    return n.Host.Close()
}
