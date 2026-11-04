package chain

import (
	"encoding/json"
	"math"
	"testing"
)

// FuzzBlockRequestWindow decodes a qsdm-blocks message the way the block
// propagator's read loop does and, for a catch-up request, computes the
// window handleBlockRequest asks its block provider for. Whatever a peer
// sends, the window must lie inside the requested range and hold at most the
// node's response limit.
func FuzzBlockRequestWindow(f *testing.F) {
	request := func(from, to, limit uint64) []byte {
		payload, _ := json.Marshal(BlockRequest{From: from, To: to, Limit: limit})
		raw, _ := json.Marshal(BlockP2PMessage{Kind: BlockMessageRequest, Payload: payload, OriginNode: "peer-1",
			RequestID: "20260801T120000.000000000Z", Timestamp: "2026-08-01T12:00:00Z"})
		return raw
	}
	for _, s := range []struct {
		raw []byte
		max int
	}{
		{request(100, 163, 64), 64},
		{request(100, 99, 0), 64},
		{request(0, math.MaxUint64, math.MaxUint64), 64},
		{request(math.MaxUint64-3, math.MaxUint64, 10), 64},
		{request(math.MaxUint64, 0, 1), 0},
		{request(5, 5000, 1000), 128},
		{request(7, 9, 2), -1},
		{[]byte(`{"kind":"block_request","payload":{"from":1,"to":-1}}`), 64},
		{[]byte(`{"kind":"block_request","payload":null}`), 64},
	} {
		f.Add(s.raw, s.max)
	}

	f.Fuzz(func(t *testing.T, raw []byte, maxResponse int) {
		var msg BlockP2PMessage
		if json.Unmarshal(raw, &msg) != nil || msg.Kind != BlockMessageRequest {
			return
		}
		var req BlockRequest
		if json.Unmarshal(msg.Payload, &req) != nil {
			return
		}
		bp := &BlockPropagator{}
		bp.SetMaxResponseBlocks(maxResponse)
		max := uint64(bp.maxResponseBlocks)
		if maxResponse > 0 && max != uint64(maxResponse) || maxResponse <= 0 && max != defaultBlockResponseLimit {
			t.Fatalf("response limit %d configured as %d", maxResponse, max)
		}

		// handleBlockRequest's arithmetic, step by step.
		from, to := req.From, req.To
		if to < from {
			to = from
		}
		limit := bp.responseLimit(req.Limit)
		end := clampBlockRequestTo(from, to, limit)

		if limit < 1 || limit > max || req.Limit >= 1 && req.Limit <= max && limit != req.Limit {
			t.Fatalf("limit %d for a request of %d with a cap of %d", limit, req.Limit, max)
		}
		if end < from || end > to {
			t.Fatalf("window [%d, %d] is outside the requested [%d, %d]", from, end, from, to)
		}
		if span := end - from; span >= limit || (to-from < limit && end != to) || (to-from >= limit && span != limit-1) {
			t.Fatalf("window [%d, %d] for [%d, %d] with limit %d", from, end, from, to, limit)
		}
	})
}
