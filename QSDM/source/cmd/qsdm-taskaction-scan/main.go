// Command qsdm-taskaction-scan reports what an operator needs before enabling
// task-action signature enforcement.
//
// [consensus] task_action_signature_activation_height rejects any unsigned
// qsdm/tasks/v1 transaction at or above the height it is set to. If the chain
// already carries one above that value, replay diverges and the node cannot
// follow. So the height has to sit above the last unsigned task action AND
// above the tip -- and until now the only instruction was "go and count them",
// which is a measurement, not a command.
//
// This is the command. It is read-only: it opens the chain file, counts, and
// prints. It never writes, and it never touches config.
//
//	qsdm-taskaction-scan --chain /opt/qsdm/qsdm_chain.ndjson
//
// Run it on every node. They should agree; if they do not, that disagreement
// matters more than the number.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

func main() {
	chainPath := flag.String("chain", "", "path to the chain NDJSON file (one block per line)")
	margin := flag.Uint64("margin", 1000, "blocks of headroom to add above the tip when suggesting an activation height")
	flag.Parse()

	if strings.TrimSpace(*chainPath) == "" {
		fmt.Fprintln(os.Stderr, "--chain is required")
		flag.Usage()
		os.Exit(2)
	}
	os.Exit(run(*chainPath, *margin, os.Stdout, os.Stderr))
}

// run does the work and returns the process exit code.
//
// Split out from main so the REFUSING branch is reachable by a test. It was
// not: the first version put the guard directly in main(), and the test called
// scanChain and asserted it returned zeros -- which it did before the guard
// existed too. Neutering the guard left every test passing, and a reviewer
// demonstrated the original bug reproducing against the neutered binary.
// A guard nothing exercises is the defect it was written to fix.
func run(chainPath string, margin uint64, stdout, stderr io.Writer) int {
	res, err := scanChain(chainPath)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}

	// Refuse to advise on a chain we did not read. An empty file, a wrong
	// path, an unmounted volume or an unsynced state dir all produce zero
	// blocks -- and a zero-block report is indistinguishable at a glance from
	// a genuinely fresh chain, because both would print a confident height.
	if res.blocks == 0 {
		fmt.Fprintf(stderr, "REFUSING TO ADVISE: no blocks were read from %s\n", chainPath)
		fmt.Fprintln(stderr, "An empty or unreadable chain file looks exactly like a clean chain here.")
		fmt.Fprintln(stderr, "Check the path, that the volume is mounted, and that the node has synced.")
		return 1
	}

	fmt.Fprintf(stdout, "blocks scanned            %d\n", res.blocks)
	fmt.Fprintf(stdout, "tip height                %d\n", res.tipHeight)
	fmt.Fprintf(stdout, "qsdm/tasks/v1 txs         %d\n", res.taskActions)
	fmt.Fprintf(stdout, "  of those, unsigned      %d\n", res.unsigned)
	if res.haveUnsigned {
		fmt.Fprintf(stdout, "  highest unsigned at     %d\n", res.lastUnsignedAt)
	} else {
		fmt.Fprintf(stdout, "  highest unsigned at     none\n")
	}

	floor := res.tipHeight
	if res.haveUnsigned && res.lastUnsignedAt > floor {
		floor = res.lastUnsignedAt
	}
	suggested := floor + margin
	fmt.Fprintln(stdout)
	fmt.Fprintf(stdout, "suggested task_action_signature_activation_height = %d\n", suggested)
	fmt.Fprintf(stdout, "  (above the tip and above the last unsigned task action, plus %d blocks of headroom)\n", margin)
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Set the SAME value on every node before the chain reaches it.")
	fmt.Fprintln(stdout, "A node with a different value will diverge on replay.")

	if res.unsigned > 0 {
		fmt.Fprintln(stdout)
		fmt.Fprintf(stdout, "NOTE: %d unsigned task action(s) already exist on this chain. They will keep\n", res.unsigned)
		fmt.Fprintln(stdout, "replaying fine below the activation height. Choosing a height at or below")
		fmt.Fprintf(stdout, "%d would reject them and stop this node following the chain.\n", res.lastUnsignedAt)
	}
	return 0
}

// scanResult is what an operator needs to choose an activation height.
type scanResult struct {
	blocks         uint64
	tipHeight      uint64
	taskActions    uint64
	unsigned       uint64
	lastUnsignedAt uint64
	haveUnsigned   bool
}

// scanChain walks a chain NDJSON file. Split out from main so it is testable:
// the dangerous failure here is a silent zero -- a scanner that misreads the
// format reports a clean chain, and the operator then picks an activation
// height that rejects real transactions.
func scanChain(path string) (scanResult, error) {
	var res scanResult
	f, err := os.Open(path) // #nosec G304 -- operator-supplied path, read-only
	if err != nil {
		return res, fmt.Errorf("open chain: %w", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 64*1024*1024)
	line := 0
	for sc.Scan() {
		line++
		raw := sc.Bytes()
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var b chain.Block
		if err := json.Unmarshal(raw, &b); err != nil {
			return res, fmt.Errorf("parse line %d: %w", line, err)
		}
		res.blocks++
		if b.Height > res.tipHeight {
			res.tipHeight = b.Height
		}
		for _, tx := range b.Transactions {
			if tx == nil || tx.ContractID != chain.TaskContractID {
				continue
			}
			res.taskActions++
			// The exact condition VerifyTaskActionSignature treats as unsigned,
			// so this count means what the gate will act on.
			if tx.Signature == "" || tx.PublicKey == "" {
				res.unsigned++
				if b.Height >= res.lastUnsignedAt {
					res.lastUnsignedAt = b.Height
					res.haveUnsigned = true
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return res, fmt.Errorf("read chain: %w", err)
	}
	return res, nil
}
