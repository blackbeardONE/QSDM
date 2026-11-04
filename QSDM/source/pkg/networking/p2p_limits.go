package networking

import (
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	rcmgr "github.com/libp2p/go-libp2p/p2p/host/resource-manager"
	"github.com/libp2p/go-libp2p/p2p/net/connmgr"
)

// TxTopicName is the GossipSub topic carrying signed transactions and signed
// enrollment envelopes (joined directly by SetupLibP2PWithPortBindAndKeyAndNAT).
const TxTopicName = "qsdm-transactions"

// Gossip topic names owned by other packages. They are repeated here (rather
// than imported) because pkg/chain and pkg/bridge must not be imported by the
// transport layer just to name a topic; p2p_limits_test.go pins them against
// the source constants.
const (
	blockTopicNameForLimits  = "qsdm-blocks"
	bridgeTopicNameForLimits = "qsdm-bridge"
)

// Default limits. They are deliberately conservative for a small validator
// network (a handful of followers per public node) and far above anything a
// loopback-only single node ever uses. See QSDM/docs/docs/P2P_LIMITS.md for the
// rationale behind each number.
const (
	DefaultP2PConnLowWater          = 64
	DefaultP2PConnHighWater         = 96
	DefaultP2PConnGracePeriod       = time.Minute
	DefaultP2PMaxConns              = 192
	DefaultP2PMaxInboundConns       = 128
	DefaultP2PMaxStreams            = 2048
	DefaultP2PMaxInboundStreams     = 1024
	DefaultP2PMaxConnsPerPeer       = 8
	DefaultP2PMaxStreamsPerPeer     = 256
	DefaultP2PMaxInboundStreamsPeer = 128
	DefaultP2PMaxConnsPerIP         = 8
	DefaultGossipMaxMessageBytes    = pubsub.DefaultMaxMessageSize // 1 MiB, unchanged from the library default
	DefaultGossipPeerRate           = 100.0                        // messages / second / peer / topic
	DefaultGossipPeerBurst          = 1000
	DefaultGossipValidateQueue      = 256
	DefaultGossipValidateThrottle   = 2048
)

// DefaultGossipTopicMaxBytes returns the per-topic payload ceilings. A topic
// not listed here is bounded only by the global GossipMaxMessageBytes.
func DefaultGossipTopicMaxBytes() map[string]int {
	return map[string]int{
		TxTopicName:              256 << 10, // signed tx / enrollment envelope (ML-DSA sig + key + payload)
		BFTTopicName:             1 << 20,   // a propose carries the full block body
		blockTopicNameForLimits:  1 << 20,   // new_block / block_response (up to 64 blocks)
		EvidenceTopicName:        256 << 10, // two conflicting authenticated votes
		PolTopicName:             512 << 10, // prevote-lock proof / round certificate (one sig per validator)
		bridgeTopicNameForLimits: 64 << 10,  // small lock/swap JSON events
	}
}

// P2PLimits bounds the libp2p transport (connection manager + resource
// manager) and the GossipSub layer (message sizes, per-peer rate, peer
// scoring, validation queue). Zero values mean "use the default"; see
// WithDefaults. Negative values for GossipPeerRate disable the per-peer rate
// limit.
type P2PLimits struct {
	// Disabled restores the behaviour from before these limits: libp2p's
	// built-in autoscaled resource manager and 160/192 connection manager,
	// no gossip validator, no peer scoring. Escape hatch only.
	Disabled bool

	ConnLowWater    int
	ConnHighWater   int
	ConnGracePeriod time.Duration

	MaxConns          int
	MaxInboundConns   int
	MaxStreams        int
	MaxInboundStreams int

	MaxConnsPerPeer          int
	MaxStreamsPerPeer        int
	MaxInboundStreamsPerPeer int
	MaxConnsPerIP            int

	GossipMaxMessageBytes int
	// GossipTopicMaxBytes overrides the per-topic ceilings; merged over
	// DefaultGossipTopicMaxBytes. Values are clamped to GossipMaxMessageBytes.
	GossipTopicMaxBytes map[string]int
	GossipPeerRate      float64
	GossipPeerBurst     int

	GossipPeerScoringDisabled bool
	GossipValidateQueue       int
	GossipValidateThrottle    int
}

// DefaultP2PLimits returns the fully populated default limits.
func DefaultP2PLimits() P2PLimits {
	return P2PLimits{}.WithDefaults()
}

// WithDefaults returns a copy with every zero field replaced by its default.
func (l P2PLimits) WithDefaults() P2PLimits {
	def := func(v *int, d int) {
		if *v == 0 {
			*v = d
		}
	}
	def(&l.ConnLowWater, DefaultP2PConnLowWater)
	def(&l.ConnHighWater, DefaultP2PConnHighWater)
	if l.ConnGracePeriod == 0 {
		l.ConnGracePeriod = DefaultP2PConnGracePeriod
	}
	def(&l.MaxConns, DefaultP2PMaxConns)
	def(&l.MaxInboundConns, DefaultP2PMaxInboundConns)
	def(&l.MaxStreams, DefaultP2PMaxStreams)
	def(&l.MaxInboundStreams, DefaultP2PMaxInboundStreams)
	def(&l.MaxConnsPerPeer, DefaultP2PMaxConnsPerPeer)
	def(&l.MaxStreamsPerPeer, DefaultP2PMaxStreamsPerPeer)
	def(&l.MaxInboundStreamsPerPeer, DefaultP2PMaxInboundStreamsPeer)
	def(&l.MaxConnsPerIP, DefaultP2PMaxConnsPerIP)
	def(&l.GossipMaxMessageBytes, DefaultGossipMaxMessageBytes)
	if l.GossipPeerRate == 0 {
		l.GossipPeerRate = DefaultGossipPeerRate
	}
	def(&l.GossipPeerBurst, DefaultGossipPeerBurst)
	def(&l.GossipValidateQueue, DefaultGossipValidateQueue)
	def(&l.GossipValidateThrottle, DefaultGossipValidateThrottle)

	merged := DefaultGossipTopicMaxBytes()
	for topic, n := range l.GossipTopicMaxBytes {
		topic = strings.TrimSpace(topic)
		if topic == "" || n <= 0 {
			continue
		}
		merged[topic] = n
	}
	for topic, n := range merged {
		if n > l.GossipMaxMessageBytes {
			merged[topic] = l.GossipMaxMessageBytes
		}
	}
	l.GossipTopicMaxBytes = merged
	return l
}

// Validate checks a (defaulted) limit set for internal consistency.
func (l P2PLimits) Validate() error {
	if l.Disabled {
		return nil
	}
	l = l.WithDefaults()
	pos := []struct {
		name string
		v    int
	}{
		{"conn_low_water", l.ConnLowWater},
		{"conn_high_water", l.ConnHighWater},
		{"max_conns", l.MaxConns},
		{"max_inbound_conns", l.MaxInboundConns},
		{"max_streams", l.MaxStreams},
		{"max_inbound_streams", l.MaxInboundStreams},
		{"max_conns_per_peer", l.MaxConnsPerPeer},
		{"max_streams_per_peer", l.MaxStreamsPerPeer},
		{"max_inbound_streams_per_peer", l.MaxInboundStreamsPerPeer},
		{"max_conns_per_ip", l.MaxConnsPerIP},
		{"gossip_max_message_bytes", l.GossipMaxMessageBytes},
		{"gossip_peer_burst", l.GossipPeerBurst},
		{"gossip_validate_queue", l.GossipValidateQueue},
		{"gossip_validate_throttle", l.GossipValidateThrottle},
	}
	for _, p := range pos {
		if p.v < 0 {
			return fmt.Errorf("p2p limit %s must be > 0 (0 = default), got %d", p.name, p.v)
		}
	}
	if l.ConnGracePeriod < 0 {
		return fmt.Errorf("p2p limit conn_grace_period must be >= 0, got %s", l.ConnGracePeriod)
	}
	if l.ConnLowWater >= l.ConnHighWater {
		return fmt.Errorf("p2p limit conn_low_water (%d) must be < conn_high_water (%d)", l.ConnLowWater, l.ConnHighWater)
	}
	if l.MaxConns < l.ConnHighWater {
		return fmt.Errorf("p2p limit max_conns (%d) must be >= conn_high_water (%d) so the connection manager trims before the resource manager refuses", l.MaxConns, l.ConnHighWater)
	}
	if l.MaxInboundConns > l.MaxConns {
		return fmt.Errorf("p2p limit max_inbound_conns (%d) must be <= max_conns (%d)", l.MaxInboundConns, l.MaxConns)
	}
	if l.MaxInboundStreams > l.MaxStreams {
		return fmt.Errorf("p2p limit max_inbound_streams (%d) must be <= max_streams (%d)", l.MaxInboundStreams, l.MaxStreams)
	}
	if l.MaxInboundStreamsPerPeer > l.MaxStreamsPerPeer {
		return fmt.Errorf("p2p limit max_inbound_streams_per_peer (%d) must be <= max_streams_per_peer (%d)", l.MaxInboundStreamsPerPeer, l.MaxStreamsPerPeer)
	}
	return nil
}

// String renders the effective limits for the startup log.
func (l P2PLimits) String() string {
	if l.Disabled {
		return "disabled (libp2p library defaults)"
	}
	l = l.WithDefaults()
	topics := make([]string, 0, len(l.GossipTopicMaxBytes))
	for t, n := range l.GossipTopicMaxBytes {
		topics = append(topics, fmt.Sprintf("%s=%d", t, n))
	}
	sort.Strings(topics)
	return fmt.Sprintf("connmgr=%d/%d grace=%s conns=%d(in %d) streams=%d(in %d) per_peer_conns=%d per_peer_streams=%d(in %d) per_ip_conns=%d gossip_max=%d topics=[%s] peer_rate=%.0f/s burst=%d scoring=%t",
		l.ConnLowWater, l.ConnHighWater, l.ConnGracePeriod, l.MaxConns, l.MaxInboundConns,
		l.MaxStreams, l.MaxInboundStreams, l.MaxConnsPerPeer, l.MaxStreamsPerPeer, l.MaxInboundStreamsPerPeer,
		l.MaxConnsPerIP, l.GossipMaxMessageBytes, strings.Join(topics, " "), l.GossipPeerRate, l.GossipPeerBurst,
		!l.GossipPeerScoringDisabled)
}

// resourceLimitConfig builds the concrete resource-manager limits: libp2p's
// autoscaled defaults (memory, FDs, transient and service scopes) with the
// connection and stream counts replaced by the explicit QSDM limits.
func (l P2PLimits) resourceLimitConfig() rcmgr.ConcreteLimitConfig {
	scaling := rcmgr.DefaultLimits
	libp2p.SetDefaultServiceLimits(&scaling)
	base := scaling.AutoScale()
	partial := rcmgr.PartialLimitConfig{
		System: rcmgr.ResourceLimits{
			Conns:           rcmgr.LimitVal(l.MaxConns),
			ConnsInbound:    rcmgr.LimitVal(l.MaxInboundConns),
			ConnsOutbound:   rcmgr.LimitVal(l.MaxConns),
			Streams:         rcmgr.LimitVal(l.MaxStreams),
			StreamsInbound:  rcmgr.LimitVal(l.MaxInboundStreams),
			StreamsOutbound: rcmgr.LimitVal(l.MaxStreams),
		},
		PeerDefault: rcmgr.ResourceLimits{
			Conns:           rcmgr.LimitVal(l.MaxConnsPerPeer),
			ConnsInbound:    rcmgr.LimitVal(l.MaxConnsPerPeer),
			ConnsOutbound:   rcmgr.LimitVal(l.MaxConnsPerPeer),
			Streams:         rcmgr.LimitVal(l.MaxStreamsPerPeer),
			StreamsInbound:  rcmgr.LimitVal(l.MaxInboundStreamsPerPeer),
			StreamsOutbound: rcmgr.LimitVal(l.MaxStreamsPerPeer),
		},
	}
	return partial.Build(base)
}

// newResourceManager builds the libp2p resource manager for these limits.
// Loopback (127.0.0.0/8, ::1) keeps libp2p's built-in unlimited per-IP
// allowance, so a loopback-only node is never affected by MaxConnsPerIP.
func (l P2PLimits) newResourceManager() (network.ResourceManager, libp2p.Option, error) {
	mgr, err := rcmgr.NewResourceManager(
		rcmgr.NewFixedLimiter(l.resourceLimitConfig()),
		rcmgr.WithLimitPerSubnet(
			[]rcmgr.ConnLimitPerSubnet{{PrefixLength: 32, ConnCount: l.MaxConnsPerIP}},
			[]rcmgr.ConnLimitPerSubnet{
				{PrefixLength: 56, ConnCount: l.MaxConnsPerIP},
				{PrefixLength: 48, ConnCount: 8 * l.MaxConnsPerIP},
			},
		),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("libp2p resource manager: %w", err)
	}
	return mgr, libp2p.ResourceManager(mgr), nil
}

// hostOptions returns the libp2p options (resource manager + connection
// manager) for these limits. The limits must already be defaulted. Once
// passed to libp2p.New, both managers are owned (and closed) by the host.
func (l P2PLimits) hostOptions() ([]libp2p.Option, error) {
	if l.Disabled {
		return nil, nil
	}
	mgr, rmOpt, err := l.newResourceManager()
	if err != nil {
		return nil, err
	}
	cm, err := connmgr.NewConnManager(l.ConnLowWater, l.ConnHighWater, connmgr.WithGracePeriod(l.ConnGracePeriod))
	if err != nil {
		_ = mgr.Close()
		return nil, fmt.Errorf("libp2p connection manager: %w", err)
	}
	return []libp2p.Option{rmOpt, libp2p.ConnectionManager(cm)}, nil
}

// pubsubOptions returns the GossipSub options for these limits. The guard
// (built with the local host ID) always accepts locally published messages so a
// node's own publications (including the single-node producer's) are never
// affected by the inbound limits.
func (l P2PLimits) pubsubOptions(guard *gossipGuard) []pubsub.Option {
	if l.Disabled {
		return nil
	}
	opts := []pubsub.Option{
		pubsub.WithMaxMessageSize(l.GossipMaxMessageBytes),
		pubsub.WithValidateQueueSize(l.GossipValidateQueue),
		pubsub.WithValidateThrottle(l.GossipValidateThrottle),
		pubsub.WithDefaultValidator(pubsub.ValidatorEx(guard.validate), pubsub.WithValidatorInline(true)),
	}
	if !l.GossipPeerScoringDisabled {
		params, thresholds := gossipPeerScoreParams(l.GossipTopicMaxBytes)
		opts = append(opts, pubsub.WithPeerScore(params, thresholds))
	}
	return opts
}

// Gossip peer-score thresholds. A peer is only penalised for messages our
// validator REJECTS (oversized for its topic) and for GossipSub protocol
// misbehaviour (P7: broken IWANT promises, GRAFT during backoff) and IP
// colocation (P6). Honest peers sit at score 0 and are never affected.
const (
	gossipScoreGossipThreshold   = -500.0
	gossipScorePublishThreshold  = -1000.0
	gossipScoreGraylistThreshold = -2500.0
)

func gossipPeerScoreParams(topicMax map[string]int) (*pubsub.PeerScoreParams, *pubsub.PeerScoreThresholds) {
	_, lo4, _ := net.ParseCIDR("127.0.0.0/8")
	_, lo6, _ := net.ParseCIDR("::1/128")
	topics := make(map[string]*pubsub.TopicScoreParams, len(topicMax))
	for topic := range topicMax {
		topics[topic] = &pubsub.TopicScoreParams{
			TopicWeight: 1,
			// P1 is disabled (weight 0) but gossipsub still divides by the
			// quantum for any in-mesh peer, so it must be non-zero.
			TimeInMeshQuantum:              time.Second,
			InvalidMessageDeliveriesWeight: -10,
			InvalidMessageDeliveriesDecay:  pubsub.ScoreParameterDecay(10 * time.Minute),
			SkipAtomicValidation:           true,
		}
	}
	params := &pubsub.PeerScoreParams{
		Topics:                      topics,
		AppSpecificScore:            func(peer.ID) float64 { return 0 },
		AppSpecificWeight:           1,
		IPColocationFactorWeight:    -10,
		IPColocationFactorThreshold: DefaultP2PMaxConnsPerIP,
		IPColocationFactorWhitelist: []*net.IPNet{lo4, lo6},
		BehaviourPenaltyWeight:      -10,
		BehaviourPenaltyThreshold:   6,
		BehaviourPenaltyDecay:       pubsub.ScoreParameterDecay(10 * time.Minute),
		DecayInterval:               pubsub.DefaultDecayInterval,
		DecayToZero:                 pubsub.DefaultDecayToZero,
		RetainScore:                 10 * time.Minute,
	}
	thresholds := &pubsub.PeerScoreThresholds{
		GossipThreshold:             gossipScoreGossipThreshold,
		PublishThreshold:            gossipScorePublishThreshold,
		GraylistThreshold:           gossipScoreGraylistThreshold,
		AcceptPXThreshold:           100,
		OpportunisticGraftThreshold: 5,
	}
	return params, thresholds
}
