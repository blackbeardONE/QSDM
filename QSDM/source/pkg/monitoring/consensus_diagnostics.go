package monitoring

import "github.com/blackbeardONE/QSDM/pkg/chain"

// ConsensusDiagnosticsCollector exposes enough provenance to prevent a
// singleton synthetic preseal from being mistaken for peer-vote consensus.
func ConsensusDiagnosticsCollector(
	role string,
	activeValidators func() int,
	syntheticStats func() chain.SyntheticPresealStats,
	peerVoteCommits func() uint64,
	peerVoteReactorReady bool,
) MetricCollector {
	return func() []Metric {
		active := 0
		if activeValidators != nil {
			active = activeValidators()
		}
		mode := "follower_no_local_seal"
		switch {
		case role == "solo":
			mode = "solo_no_preseal"
		case role == "network-producer" && active <= 1:
			mode = "synthetic_singleton"
		case role == "network-producer":
			mode = "peer_vote_required"
		}

		stats := chain.SyntheticPresealStats{}
		if syntheticStats != nil {
			stats = syntheticStats()
		}
		var peerCommits uint64
		if peerVoteCommits != nil {
			peerCommits = peerVoteCommits()
		}
		reactorReady := float64(0)
		if peerVoteReactorReady {
			reactorReady = 1
		}

		return []Metric{
			{
				Name:   "qsdm_consensus_preseal_mode_info",
				Help:   "Active local sealing mode; peer_vote_required is not live until reactor_ready is 1",
				Type:   MetricGauge,
				Value:  1,
				Labels: map[string]string{"mode": mode, "role": role},
			},
			{Name: "qsdm_consensus_active_validators", Help: "Active validators in the committed local projection", Type: MetricGauge, Value: float64(active)},
			{Name: "qsdm_consensus_synthetic_preseal_attempts_total", Help: "Singleton synthetic preseal attempts", Type: MetricCounter, Value: float64(stats.Attempts)},
			{Name: "qsdm_consensus_synthetic_preseal_commits_total", Help: "Commits completed by singleton synthetic preseal", Type: MetricCounter, Value: float64(stats.Commits)},
			{Name: "qsdm_consensus_synthetic_preseal_rejected_multivalidator_total", Help: "Synthetic preseal attempts rejected because the validator set was not singleton", Type: MetricCounter, Value: float64(stats.RejectedMultivalidator)},
			{Name: "qsdm_consensus_peer_vote_commits_total", Help: "Commits first observed from inbound peer precommits", Type: MetricCounter, Value: float64(peerCommits)},
			{Name: "qsdm_consensus_peer_vote_reactor_ready", Help: "Whether live multi-validator proposal and vote production is wired", Type: MetricGauge, Value: reactorReady},
		}
	}
}
