package chain

import "testing"

func TestEncodeDecodePrevoteLockProof(t *testing.T) {
	p := &PrevoteLockProof{
		Height:          9,
		Round:           2,
		LockedBlockHash: "abc",
		CarriedFromLock: "prev",
		Prevotes: []BlockVote{
			{Validator: "v1", BlockHash: "abc", Height: 9, Round: 2, Type: VotePreVote},
		},
	}
	raw, err := EncodePrevoteLockProof(p)
	if err != nil || len(raw) == 0 {
		t.Fatal(err)
	}
	out, err := DecodePrevoteLockProof(raw)
	if err != nil {
		t.Fatal(err)
	}
	if out.Height != 9 || out.LockedBlockHash != "abc" || out.CarriedFromLock != "prev" || len(out.Prevotes) != 1 {
		t.Fatalf("round-trip mismatch: %+v", out)
	}
}
