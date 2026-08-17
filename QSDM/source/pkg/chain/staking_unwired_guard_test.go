package chain

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Critical #2 in the capability audit -- "staking txs unauthenticated at apply"
// -- names code that does not exist on main's lineage.
//
// The audit's fix line says to "mirror the wallet_transfer.go pattern in
// ApplyStakingTx", citing staking_tx.go and handlers_staking.go. Neither file
// is on main or on this branch; they belong to agent/multi-mother-hive-release,
// which is not an ancestor of main (audit §0). What ships is StakingLedger, and
// its two value-moving methods -- Delegate and BeginUnbond -- have ZERO
// production callers. There is no staking transaction to authenticate because
// there is no staking transaction.
//
// That makes the row unreachable rather than open, and this file is the
// evidence for saying so. But "unreachable today" is exactly the state 2b was
// in before someone wired it: an unauthenticated mutator sitting in the tree,
// waiting for a caller. Delegate and BeginUnbond take a delegator address as a
// plain string and move CELL out of that account with no signature, no public
// key and no nonce bump. Wiring either to a transaction type or an HTTP handler
// without adding authentication first would reintroduce the critical in its
// original form.
//
// So this guard fails the moment a production caller appears. It is not
// asserting the functions are correct; it is asserting nobody has quietly
// removed the reason the audit row can be closed.
func TestStakingLedger_valueMoversHaveNoProductionCallers(t *testing.T) {
	root := repoRootForStakingGuard(t)

	// Method calls, not declarations: a receiver expression then .Delegate( or
	// .BeginUnbond(. The declarations in staking_ledger.go read
	// "func (s *StakingLedger) Delegate(" and do not match.
	callSite := regexp.MustCompile(`\.\s*(Delegate|BeginUnbond)\s*\(`)

	var offenders []string
	for _, rel := range gitLsFilesForStakingGuard(t, root, "*.go") {
		if strings.HasSuffix(rel, "_test.go") {
			continue
		}
		// .cache holds detached worktrees of other branches, which DO carry the
		// staking module. They are not this tree.
		if strings.Contains(rel, "/.cache/") || strings.HasPrefix(rel, ".cache/") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			continue
		}
		for i, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
			if strings.HasPrefix(line, "//") {
				continue
			}
			if callSite.MatchString(line) {
				offenders = append(offenders, rel+":"+itoa(i+1)+": "+line)
			}
		}
	}

	if len(offenders) > 0 {
		t.Fatalf("StakingLedger.Delegate/BeginUnbond now have production callers:\n  %s\n\n"+
			"These methods move CELL out of a delegator account identified by a bare string, "+
			"with no signature check, no public key and no nonce bump. Before wiring them, "+
			"authenticate the caller the way wallet_transfer.go:41-48 and enrollment_apply.go:202 "+
			"do at replay -- carry Signature/PublicKey through to apply and use DebitAndBumpNonce. "+
			"Then update audit critical #2, which this test is the evidence for.",
			strings.Join(offenders, "\n  "))
	}
}

// The audit says grep Signature returns 0 hits in the staking files. That was
// measured on a branch carrying staking_tx.go. Pin it for what actually ships,
// so the claim in the audit is checkable rather than inherited.
func TestStakingLedger_shippedFilesCarryNoSignatureHandling(t *testing.T) {
	for _, name := range []string{"staking_ledger.go", "staking_persist.go", "staking_registry.go"} {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(b), "Signature") {
			t.Errorf("%s now mentions Signature. If authentication was added, say so in "+
				"audit critical #2 and relax TestStakingLedger_valueMoversHaveNoProductionCallers, "+
				"which currently blocks wiring precisely because no such handling exists.", name)
		}
	}
}

func repoRootForStakingGuard(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func gitLsFilesForStakingGuard(t *testing.T, root string, patterns ...string) []string {
	t.Helper()
	args := append([]string{"-C", root, "ls-files"}, patterns...)
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		t.Skipf("git ls-files unavailable: %v", err)
	}
	return strings.Fields(string(out))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
