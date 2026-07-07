// Command qsdm-cell-sim is an offline economic simulator for the Sky Fang ↔ CELL
// play-to-earn model. It answers the only question that matters before launch:
// "is the CELL Reward Pool (CRP) sustainable, and how much CELL does a real player
// earn per season at a given player count and funding rate?"
//
// MODEL (matches apps/game-integration/ECONOMIC_MODEL.md, earn-only):
//
//   - CELL is fair-launch and the studio NEVER mints it. To fund rewards, the
//     studio spends a fraction (RPFR) of its Ingot (real-money) revenue to BUY
//     CELL on the open market and routes it into the CRP. So pool inflow per epoch
//     = (IngotRevenueUSD * RPFR) / CellPriceUSD.
//   - Each epoch the bridge distributes the pool proportionally to Contribution
//     Score, clamping each account to MaxCellPerAccount. Leftover from clamping is
//     NOT redistributed; it carries into the next epoch (matches QsdmBridge).
//   - Players spend earned CELL on cosmetics; SRR of that spend is recycled back
//     into the pool, the rest is the studio's marketplace take.
//
// This is a planning tool, not chain logic. Deterministic given --seed so the
// test can assert invariants (per-account cap respected, pool bounded, payouts > 0).
//
// Usage:
//
//	go run ./cmd/qsdm-cell-sim --players 5000 --revenue 20000 --rpfr 0.30 --cap 25
//	go run ./cmd/qsdm-cell-sim --json   # machine-readable per-epoch stats
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"text/tabwriter"
)

// Params are the simulation inputs (all per-epoch unless noted).
type Params struct {
	Epochs            int     `json:"epochs"`
	Players           int     `json:"players"`
	IngotRevenueUSD   float64 `json:"ingot_revenue_usd"`    // studio real-money revenue per epoch
	CellPriceUSD      float64 `json:"cell_price_usd"`       // market price of 1 CELL
	RPFR              float64 `json:"rpfr"`                 // 0..1 reward-pool funding rate
	MaxCellPerAccount float64 `json:"max_cell_per_account"` // per-epoch payout clamp
	SeedPoolCell      float64 `json:"seed_pool_cell"`       // initial CRP balance
	MarketParticip    float64 `json:"market_participation"` // 0..1 fraction who buy each epoch
	AvgBasketCell     float64 `json:"avg_basket_cell"`      // avg CELL a buyer spends
	SRR               float64 `json:"srr"`                  // 0..1 sink recycle rate
	ActiveRate        float64 `json:"active_rate"`          // 0..1 fraction active each epoch
	Seed              int64   `json:"seed"`
}

// DefaultParams is a plausible mid-size F2P starting point.
func DefaultParams() Params {
	return Params{
		Epochs:            26, // ~half a year of weekly seasons
		Players:           5000,
		IngotRevenueUSD:   20000,
		CellPriceUSD:      0.50,
		RPFR:              0.30,
		MaxCellPerAccount: 25,
		SeedPoolCell:      0,
		MarketParticip:    0.20,
		AvgBasketCell:     6,
		SRR:               0.50,
		ActiveRate:        0.70,
		Seed:              42,
	}
}

// EpochStat is the per-epoch outcome.
type EpochStat struct {
	Epoch         int     `json:"epoch"`
	ActivePlayers int     `json:"active_players"`
	FundingCell   float64 `json:"funding_cell"`
	PoolBefore    float64 `json:"pool_before"`
	Distributed   float64 `json:"distributed"`
	PoolAfter     float64 `json:"pool_after"`
	AvgPerActive  float64 `json:"avg_per_active"`
	CappedCount   int     `json:"capped_count"`
	MarketSpent   float64 `json:"market_spent"`
	Recycled      float64 `json:"recycled"`
	StudioTake    float64 `json:"studio_take"`
	PlayerHeld    float64 `json:"player_held"`
}

type player struct {
	score float64
	held  float64
}

// Simulate runs the model and returns per-epoch stats. Deterministic for a seed.
func Simulate(p Params) []EpochStat {
	rng := rand.New(rand.NewSource(p.Seed))

	players := make([]player, p.Players)
	for i := range players {
		// Skill/time score with a long but bounded tail (concave grinders + a few
		// stars). Exponential gives a realistic spread; the per-account cap in the
		// payout loop prevents the tail from vacuuming the pool.
		players[i].score = rng.ExpFloat64() + 0.05
	}

	pool := p.SeedPoolCell
	stats := make([]EpochStat, 0, p.Epochs)

	for e := 1; e <= p.Epochs; e++ {
		st := EpochStat{Epoch: e, PoolBefore: pool}

		// 1) Fund the pool by buying CELL with a slice of Ingot revenue.
		if p.CellPriceUSD > 0 {
			st.FundingCell = (p.IngotRevenueUSD * p.RPFR) / p.CellPriceUSD
		}
		pool += st.FundingCell
		st.PoolBefore = pool

		// 2) Pick this epoch's active set and sum their scores.
		active := make([]int, 0, p.Players)
		var totalScore float64
		for i := range players {
			if rng.Float64() < p.ActiveRate {
				active = append(active, i)
				totalScore += players[i].score
			}
		}
		st.ActivePlayers = len(active)

		// 3) Distribute pool ∝ score, clamped to the per-account cap. Leftover from
		//    clamping carries (we never redistribute it — matches the bridge).
		if totalScore > 0 && pool > 0 {
			for _, i := range active {
				share := pool * (players[i].score / totalScore)
				if share > p.MaxCellPerAccount {
					share = p.MaxCellPerAccount
					st.CappedCount++
				}
				// floor at 8dp like the bridge
				share = math.Floor(share*1e8) / 1e8
				if share <= 0 {
					continue
				}
				players[i].held += share
				st.Distributed += share
			}
			pool -= st.Distributed
		}
		st.PoolAfter = pool
		if st.ActivePlayers > 0 {
			st.AvgPerActive = st.Distributed / float64(st.ActivePlayers)
		}

		// 4) Marketplace: buyers spend earned CELL on cosmetics; SRR recycles.
		for _, i := range active {
			if rng.Float64() >= p.MarketParticip {
				continue
			}
			spend := p.AvgBasketCell
			if spend > players[i].held {
				spend = players[i].held
			}
			if spend <= 0 {
				continue
			}
			players[i].held -= spend
			st.MarketSpent += spend
		}
		st.Recycled = st.MarketSpent * p.SRR
		st.StudioTake = st.MarketSpent - st.Recycled
		pool += st.Recycled // available next epoch

		for i := range players {
			st.PlayerHeld += players[i].held
		}

		stats = append(stats, st)
	}
	return stats
}

func main() {
	p := DefaultParams()
	asJSON := flag.Bool("json", false, "emit per-epoch stats as JSON")
	flag.IntVar(&p.Epochs, "epochs", p.Epochs, "number of epochs (seasons) to simulate")
	flag.IntVar(&p.Players, "players", p.Players, "registered player base")
	flag.Float64Var(&p.IngotRevenueUSD, "revenue", p.IngotRevenueUSD, "Ingot (real-money) revenue per epoch, USD")
	flag.Float64Var(&p.CellPriceUSD, "cell-price", p.CellPriceUSD, "market price of 1 CELL, USD")
	flag.Float64Var(&p.RPFR, "rpfr", p.RPFR, "reward-pool funding rate (0..1)")
	flag.Float64Var(&p.MaxCellPerAccount, "cap", p.MaxCellPerAccount, "max CELL per account per epoch")
	flag.Float64Var(&p.SeedPoolCell, "seed-pool", p.SeedPoolCell, "initial CRP balance, CELL")
	flag.Float64Var(&p.MarketParticip, "participation", p.MarketParticip, "fraction of active players who buy each epoch (0..1)")
	flag.Float64Var(&p.AvgBasketCell, "basket", p.AvgBasketCell, "avg CELL spent per buyer")
	flag.Float64Var(&p.SRR, "srr", p.SRR, "sink recycle rate (0..1)")
	flag.Float64Var(&p.ActiveRate, "active", p.ActiveRate, "fraction of players active each epoch (0..1)")
	flag.Int64Var(&p.Seed, "seed", p.Seed, "RNG seed (deterministic)")
	flag.Parse()

	stats := Simulate(p)

	if *asJSON {
		out := map[string]any{"params": p, "epochs": stats, "summary": summarize(p, stats)}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
		return
	}

	fmt.Printf("CELL play-to-earn simulation — %d players, $%.0f/epoch revenue @ RPFR %.0f%%, CELL=$%.2f, cap %.0f\n\n",
		p.Players, p.IngotRevenueUSD, p.RPFR*100, p.CellPriceUSD, p.MaxCellPerAccount)

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "epoch\tactive\tfund(CELL)\tdistributed\tavg/active\tcapped\tmarket\trecycled\tpool_after\tplayer_held")
	for _, s := range stats {
		fmt.Fprintf(tw, "%d\t%d\t%.2f\t%.2f\t%.4f\t%d\t%.2f\t%.2f\t%.2f\t%.2f\n",
			s.Epoch, s.ActivePlayers, s.FundingCell, s.Distributed, s.AvgPerActive,
			s.CappedCount, s.MarketSpent, s.Recycled, s.PoolAfter, s.PlayerHeld)
	}
	tw.Flush()

	sum := summarize(p, stats)
	fmt.Printf("\nSummary: %s\n", sum.Verdict)
	fmt.Printf("  avg CELL/active/epoch:   %.4f  (~$%.4f)\n", sum.AvgPerActive, sum.AvgPerActive*p.CellPriceUSD)
	fmt.Printf("  pool start/end:          %.2f -> %.2f CELL\n", p.SeedPoolCell, sum.FinalPool)
	fmt.Printf("  total funded/distributed:%.2f / %.2f CELL  (utilization %.1f%%)\n",
		sum.TotalFunded, sum.TotalDistributed, sum.Utilization*100)
	fmt.Printf("  studio marketplace take: %.2f CELL (~$%.2f)\n", sum.StudioTake, sum.StudioTake*p.CellPriceUSD)
}

// Summary aggregates the run.
type Summary struct {
	AvgPerActive     float64 `json:"avg_per_active"` // CELL per active player per epoch
	AvgActive        float64 `json:"avg_active"`     // mean active players/epoch
	FinalPool        float64 `json:"final_pool"`
	TotalFunded      float64 `json:"total_funded"`
	TotalDistributed float64 `json:"total_distributed"`
	Utilization      float64 `json:"utilization"` // distributed / funded (0..~1)
	StudioTake       float64 `json:"studio_take"`
	Verdict          string  `json:"verdict"`
}

func summarize(p Params, stats []EpochStat) Summary {
	var s Summary
	if len(stats) == 0 {
		return s
	}
	var sumAvg, sumActive float64
	for _, e := range stats {
		s.TotalFunded += e.FundingCell
		s.TotalDistributed += e.Distributed
		s.StudioTake += e.StudioTake
		sumAvg += e.AvgPerActive
		sumActive += float64(e.ActivePlayers)
	}
	n := float64(len(stats))
	s.AvgPerActive = sumAvg / n
	s.AvgActive = sumActive / n
	s.FinalPool = stats[len(stats)-1].PoolAfter
	if s.TotalFunded > 0 {
		s.Utilization = s.TotalDistributed / s.TotalFunded
	}

	// Health is about payouts and balance, NOT the raw pool level — with per-epoch
	// funding the pool is *meant* to drain to ~0 each epoch (high utilization is
	// good). Two failure modes matter:
	//   OVER-FUNDED: the per-account cap binds for ~everyone yet the pool still
	//     piles up (funding > cap*activePlayers) -> idle CELL / price pressure.
	//   UNDER-FUNDED: payouts per active player are negligible -> no real incentive.
	capacity := p.MaxCellPerAccount * s.AvgActive // max distributable if all capped
	switch {
	case s.AvgActive == 0:
		s.Verdict = "STALLED: no active players to reward."
	case capacity > 0 && s.FinalPool > 3*capacity:
		s.Verdict = "OVER-FUNDED: pool balloons past payout capacity — lower RPFR or raise the cap / add sinks."
	case s.AvgPerActive < 0.02*p.MaxCellPerAccount:
		s.Verdict = "UNDER-FUNDED: payouts per player are negligible — raise RPFR or shrink the player base served."
	default:
		s.Verdict = "SUSTAINABLE: funding is fully distributed and payouts are meaningful."
	}
	return s
}
