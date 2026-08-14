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

	res, err := scanChain(*chainPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	// Refuse to advise on a chain we did not read.
	//
	// An empty file, a wrong --chain path, an unmounted volume or a state dir
	// that has not synced all produce zero blocks -- and a zero-block report is
	// indistinguishable at a glance from a genuinely fresh chain, because both
	// print a confident suggested height. That is the silent zero this tool
	// exists to avoid, and the first version of it exited 0 here.
	if res.blocks == 0 {
		fmt.Fprintf(os.Stderr, "REFUSING TO ADVISE: no blocks were read from %s\n", *chainPath)
		fmt.Fprintln(os.Stderr, "An empty or unreadable chain file looks exactly like a clean chain here.")
		fmt.Fprintln(os.Stderr, "Check the path, that the volume is mounted, and that the node has synced.")
		os.Exit(1)
	}

	blocks, tipHeight := res.blocks, res.tipHeight
	taskActions, unsigned := res.taskActions, res.unsigned
	lastUnsignedAt, haveUnsigned := res.lastUnsignedAt, res.haveUnsigned

	fmt.Printf("blocks scanned            %d\n", blocks)
	fmt.Printf("tip height                %d\n", tipHeight)
	fmt.Printf("qsdm/tasks/v1 txs         %d\n", taskActions)
	fmt.Printf("  of those, unsigned      %d\n", unsigned)
	if haveUnsigned {
		fmt.Printf("  highest unsigned at     %d\n", lastUnsignedAt)
	} else {
		fmt.Printf("  highest unsigned at     none\n")
	}

	floor := tipHeight
	if haveUnsigned && lastUnsignedAt > floor {
		floor = lastUnsignedAt
	}
	suggested := floor + *margin
	fmt.Println()
	fmt.Printf("suggested task_action_signature_activation_height = %d\n", suggested)
	fmt.Printf("  (above the tip and above the last unsigned task action, plus %d blocks of headroom)\n", *margin)
	fmt.Println()
	fmt.Println("Set the SAME value on every node before the chain reaches it.")
	fmt.Println("A node with a different value will diverge on replay.")

	if unsigned > 0 {
		fmt.Println()
		fmt.Printf("NOTE: %d unsigned task action(s) already exist on this chain. They will keep\n", unsigned)
		fmt.Println("replaying fine below the activation height. Choosing a height at or below")
		fmt.Printf("%d would reject them and stop this node following the chain.\n", lastUnsignedAt)
	}
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
