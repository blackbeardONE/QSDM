package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/blockdriver"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

type fakeHTTPChainSyncProducer struct {
	hasTip  bool
	tip     uint64
	heights []uint64
}

func (p *fakeHTTPChainSyncProducer) HasTip() bool {
	return p.hasTip
}

func (p *fakeHTTPChainSyncProducer) TipHeight() uint64 {
	return p.tip
}

func (p *fakeHTTPChainSyncProducer) TryAppendExternalBlock(blk *chain.Block) error {
	expected := uint64(0)
	if p.hasTip {
		expected = p.tip + 1
	}
	if blk.Height != expected {
		return fmt.Errorf("expected height %d, got %d", expected, blk.Height)
	}
	p.hasTip = true
	p.tip = blk.Height
	p.heights = append(p.heights, blk.Height)
	return nil
}

func TestChainSyncURLsFromEnvNormalizesAndDeduplicates(t *testing.T) {
	t.Setenv("QSDM_CHAIN_SYNC_URLS", " https://one.example/api/v1/,https://two.example/api/v1,https://one.example/api/v1 ")

	got := chainSyncURLsFromEnv()
	if len(got) != 2 {
		t.Fatalf("expected 2 URLs, got %d: %v", len(got), got)
	}
	if got[0] != "https://one.example/api/v1" || got[1] != "https://two.example/api/v1" {
		t.Fatalf("unexpected normalized URLs: %v", got)
	}
}

func TestSyncHTTPChainSourceConsumesSuccessiveWindows(t *testing.T) {
	const remoteTip = uint64(129)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		from, err := strconv.ParseUint(r.URL.Query().Get("from"), 10, 64)
		if err != nil {
			t.Fatalf("invalid from query: %v", err)
		}
		if got := r.URL.Query().Get("limit"); got != strconv.Itoa(httpChainSyncWindowBlocks) {
			t.Fatalf("unexpected limit %q", got)
		}

		to := from + httpChainSyncWindowBlocks - 1
		if to > remoteTip {
			to = remoteTip
		}
		blocks := make([]json.RawMessage, 0, to-from+1)
		if from <= remoteTip {
			for height := from; height <= to; height++ {
				raw, marshalErr := json.Marshal(chain.Block{Height: height})
				if marshalErr != nil {
					t.Fatalf("marshal block: %v", marshalErr)
				}
				blocks = append(blocks, raw)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(httpChainBlocksResponse{
			Tip:    remoteTip,
			From:   from,
			To:     to,
			Blocks: blocks,
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	producer := &fakeHTTPChainSyncProducer{}
	appended, tip, err := syncHTTPChainSource(
		context.Background(),
		server.Client(),
		producer,
		server.URL,
		8,
		nil,
	)
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	if appended != 130 || len(producer.heights) != 130 {
		t.Fatalf("expected 130 appended blocks, got appended=%d heights=%d", appended, len(producer.heights))
	}
	if tip != remoteTip || producer.tip != remoteTip {
		t.Fatalf("unexpected tips: remote=%d local=%d", tip, producer.tip)
	}
	if requests != 3 {
		t.Fatalf("expected 3 HTTP windows, got %d", requests)
	}
}

func TestSyncHTTPChainSourceHonorsWindowBudget(t *testing.T) {
	const remoteTip = uint64(999)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		from, _ := strconv.ParseUint(r.URL.Query().Get("from"), 10, 64)
		blocks := make([]json.RawMessage, 0, httpChainSyncWindowBlocks)
		for height := from; height < from+httpChainSyncWindowBlocks; height++ {
			raw, _ := json.Marshal(chain.Block{Height: height})
			blocks = append(blocks, raw)
		}
		_ = json.NewEncoder(w).Encode(httpChainBlocksResponse{
			Tip:    remoteTip,
			From:   from,
			To:     from + httpChainSyncWindowBlocks - 1,
			Blocks: blocks,
		})
	}))
	defer server.Close()

	producer := &fakeHTTPChainSyncProducer{}
	appended, tip, err := syncHTTPChainSource(
		context.Background(),
		server.Client(),
		producer,
		server.URL,
		2,
		nil,
	)
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	if appended != 2*httpChainSyncWindowBlocks {
		t.Fatalf("expected bounded append count %d, got %d", 2*httpChainSyncWindowBlocks, appended)
	}
	if tip != remoteTip {
		t.Fatalf("expected remote tip %d, got %d", remoteTip, tip)
	}
}

func TestSyncHTTPChainSourceTreatsStaleTipAsCaughtUp(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := json.Marshal(chain.Block{Height: 5})
		_ = json.NewEncoder(w).Encode(httpChainBlocksResponse{
			Tip:    5,
			From:   5,
			To:     5,
			Blocks: []json.RawMessage{raw},
		})
	}))
	defer server.Close()

	producer := &fakeHTTPChainSyncProducer{hasTip: true, tip: 6}
	appended, tip, err := syncHTTPChainSource(
		context.Background(),
		server.Client(),
		producer,
		server.URL,
		1,
		nil,
	)
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	if appended != 0 || tip != 5 || producer.tip != 6 {
		t.Fatalf("stale response changed state: appended=%d remote=%d local=%d", appended, tip, producer.tip)
	}
}

func TestPrepareCanonicalGenesisReplaySeedsPinnedOpeningAllocation(t *testing.T) {
	accounts := chain.NewAccountStore()
	blk := &chain.Block{
		Height:    0,
		Hash:      qsdmCanonicalGenesisHash,
		StateRoot: qsdmCanonicalGenesisStateRoot,
		Transactions: []*mempool.Tx{{
			ID:        "genesis-seed-1778110678195835285",
			Sender:    blockdriver.FunderAddress,
			Recipient: "qsdm1miner-rtx3050",
			Amount:    qsdmCanonicalGenesisAmount,
			Fee:       0,
			Nonce:     0,
		}},
	}

	if err := prepareCanonicalGenesisReplay(accounts, blk); err != nil {
		t.Fatalf("prepare genesis replay: %v", err)
	}
	funder, ok := accounts.Get(blockdriver.FunderAddress)
	if !ok {
		t.Fatal("canonical funder was not seeded")
	}
	want := blockdriver.DefaultFunderBalance + qsdmCanonicalGenesisAmount + qsdmCanonicalGenesisReserve
	if funder.Balance != want || funder.Nonce != 0 {
		t.Fatalf("unexpected opening funder: balance=%v nonce=%d", funder.Balance, funder.Nonce)
	}

	replay := accounts.Clone()
	if err := replay.ApplyTx(blk.Transactions[0]); err != nil {
		t.Fatalf("apply canonical genesis: %v", err)
	}
	if got := replay.StateRoot(); got != qsdmCanonicalGenesisStateRoot {
		t.Fatalf("state root=%s want=%s", got, qsdmCanonicalGenesisStateRoot)
	}
}

func TestPrepareCanonicalGenesisReplayRejectsUnknownManifest(t *testing.T) {
	accounts := chain.NewAccountStore()
	err := prepareCanonicalGenesisReplay(accounts, &chain.Block{
		Height:    0,
		Hash:      "unknown",
		StateRoot: qsdmCanonicalGenesisStateRoot,
	})
	if err == nil {
		t.Fatal("expected unknown genesis to be rejected")
	}
	if got := len(accounts.AllAccounts()); got != 0 {
		t.Fatalf("rejected genesis mutated %d accounts", got)
	}
}
