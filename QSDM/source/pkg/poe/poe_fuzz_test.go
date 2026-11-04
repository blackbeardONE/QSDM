package poe

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// fuzzParentID is the documented parent ID rule written out independently of
// ValidParentID.
func fuzzParentID(id string) bool {
	if len(id) < MinParentIDLen || len(id) > MaxParentIDLen {
		return false
	}
	for _, c := range []byte(id) {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func FuzzCheckShape(f *testing.F) {
	a, b, c := strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 32)
	f.Add("tx-0000000000000001", a+","+b)
	f.Add("tx-0000000000000001", "solo-heartbeat-805400-1791611907090994535,hive_wallet_1784876606130_1a4983ed8ce85b54")
	f.Add("self-transaction-0001", a+",self-transaction-0001")
	f.Add("x", a+","+b+","+a)
	f.Add("x", "parent1,parent2")
	f.Add("x", a)
	f.Add("x", "")
	f.Add("x", strings.Repeat(c+",", MaxParents)+c)
	f.Add("x", strings.Repeat("Z", MaxParentIDLen)+","+strings.Repeat("Z", MaxParentIDLen+1))

	f.Fuzz(func(t *testing.T, txID, parentList string) {
		var parents []string
		if parentList != "" {
			parents = strings.Split(parentList, ",")
		}
		orig := slices.Clone(parents)
		err := CheckShape(txID, parents)
		if !reflect.DeepEqual(parents, orig) {
			t.Fatal("CheckShape modified its input")
		}
		var want error
		if len(parents) < MinParents || len(parents) > MaxParents {
			want = ErrParentCount
		}
		seen := map[string]bool{}
		for _, p := range parents {
			if want != nil {
				break
			}
			if ValidParentID(p) != fuzzParentID(p) {
				t.Fatalf("ValidParentID(%q) = %v", p, ValidParentID(p))
			}
			switch {
			case !fuzzParentID(p):
				want = ErrParentFormat
			case p == txID:
				want = ErrSelfParent
			case seen[p]:
				want = ErrDuplicateParent
			}
			seen[p] = true
		}
		if want == nil {
			if err != nil {
				t.Fatalf("CheckShape(%q, %q) = %v, want nil", txID, parents, err)
			}
			return
		}
		if !errors.Is(err, want) || !IsViolation(err) || Reason(err) == "" || Reason(err) == "other" {
			t.Fatalf("CheckShape(%q, %q) = %v (reason %q), want %v", txID, parents, err, Reason(err), want)
		}
	})
}

// FuzzPickParents decodes a node's /api/v1/chain/parents (and the legacy
// /api/v1/receipts) response the way FetchParents does and checks the
// parents a wallet would then sign.
func FuzzPickParents(f *testing.F) {
	f.Add([]byte(`{"tip":7,"parents":["solo-heartbeat-7-1791611907090994535","solo-heartbeat-6-1791611897090994535"]}`))
	f.Add([]byte(`{"parents":["short","solo-heartbeat-7-1791611907090994535","solo-heartbeat-7-1791611907090994535","hive_wallet_1784876606130_1a4983ed8ce85b54"]}`))
	f.Add([]byte(`{"receipts":[{"tx_id":"solo-reward-805400-0123456789abcdef","status":1},{"tx_id":"failed-tx-000000000000","status":2},{"tx_id":"a3f1c0de9b8e7d6c5b4a39281706f5e4","status":1}]}`))
	f.Add([]byte(`{"parents":null}`))
	f.Add([]byte(`{"parents":[""]}`))
	f.Add([]byte(`[]`))

	f.Fuzz(func(t *testing.T, raw []byte) {
		var parents parentsResponse
		if json.NewDecoder(bytes.NewReader(raw)).Decode(&parents) == nil {
			checkPicked(t, parents.Parents)
		}
		var receipts receiptsResponse
		if json.NewDecoder(bytes.NewReader(raw)).Decode(&receipts) == nil {
			var ids []string
			for _, r := range receipts.Receipts {
				if r.Status == 1 {
					ids = append(ids, r.TxID)
				}
			}
			checkPicked(t, ids)
		}
	})
}

func checkPicked(t *testing.T, ids []string) {
	t.Helper()
	orig := slices.Clone(ids)
	out, err := pickParents(ids)
	if !reflect.DeepEqual(ids, orig) {
		t.Fatal("pickParents modified its input")
	}
	// The first MinParents distinct well-formed IDs, in the node's order.
	var want []string
	for _, id := range ids {
		if fuzzParentID(id) && !contains(want, id) {
			want = append(want, id)
		}
		if len(want) == MinParents {
			break
		}
	}
	if len(want) < MinParents {
		if !errors.Is(err, ErrNoParents) || out != nil {
			t.Fatalf("pickParents(%q) = %q, %v; want ErrNoParents", ids, out, err)
		}
		return
	}
	if err != nil || !reflect.DeepEqual(out, want) {
		t.Fatalf("pickParents(%q) = %q, %v; want %q", ids, out, err, want)
	}
	// Whatever a node serves, the wallet signs a parent list with a valid
	// count, well-formed IDs and no repeats.
	if err := CheckShape("", out); err != nil {
		t.Fatalf("picked parents %q fail CheckShape: %v", out, err)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
