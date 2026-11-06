package main

// The block lookups behind the paths that serve blocks (BlockProducer.GetBlock,
// BlockRange and RelayBlocks: index computations on the contiguous chain)
// select exactly what the walks they replaced selected.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// TestHL1RelayPathsOnAChainAboveGenesis: without a producer transition a
// restored journal may start above genesis. The P2P provider serves the
// contiguous blocks from `from` on, so nothing for a range that starts below
// the chain, and /chain/blocks and the headers serve the blocks inside the
// range, as before the lookups became index computations.
func TestHL1RelayPathsOnAChainAboveGenesis(t *testing.T) {
	all := hl1Chain(8)
	bp := chain.NewBlockProducer(mempool.New(mempool.DefaultConfig()), chain.NewAccountStore(), chain.DefaultProducerConfig())
	if err := bp.RestoreChain(append([]*chain.Block(nil), all[3:]...)); err != nil {
		t.Fatal(err)
	}
	durable := &hl1DurableTip{}
	durable.Store(7)
	provider := hl1BlockProvider(bp, durable)
	heights := func(blocks []*chain.Block) []uint64 {
		var hs []uint64
		for _, b := range blocks {
			hs = append(hs, b.Height)
		}
		return hs
	}
	for _, tc := range []struct {
		from, to uint64
		limit    int
		want     string
	}{
		{0, 10, 64, "[]"},
		{2, 4, 64, "[]"},
		{3, 10, 64, "[3 4 5 6 7]"},
		{5, 10, 2, "[5 6]"},
		{7, 7, 0, "[7]"},
		{8, 10, 64, "[]"},
	} {
		if got := fmt.Sprint(heights(provider(tc.from, tc.to, tc.limit))); got != tc.want {
			t.Fatalf("provider(%d, %d, %d) = %s, want %s", tc.from, tc.to, tc.limit, got, tc.want)
		}
	}
	probe := blocksProbeFromProducer(bp, durable)
	raw := probe.BlocksInRange(0, 4)
	if len(raw) != 2 || !bytes.Equal(raw[0], mustJSON(t, all[3])) || !bytes.Equal(raw[1], mustJSON(t, all[4])) {
		t.Fatalf("/chain/blocks [0, 4] served %d blocks, want 3 and 4", len(raw))
	}
	var hs []uint64
	for _, h := range probe.HeadersInRange(0, 100) {
		hs = append(hs, h.Height)
	}
	if fmt.Sprint(hs) != "[3 4 5 6 7]" {
		t.Fatalf("headers %v", hs)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
