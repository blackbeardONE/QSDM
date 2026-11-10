// Command qsdm-handover writes the node configuration for a further producer
// hand-over (QSDM/docs/docs/PRODUCER_TRANSITION.md, section 5). The
// [consensus.producer_transition] table of a node's TOML file, version 1 or
// version 2, becomes a version 2 schedule with one more epoch; nothing else in
// the file changes.
//
//	qsdm-handover --config <core.toml> --out <new.toml> \
//	    --from-height <N+1> --producer <address> --checkpoint-hash <hash of block N>
//
// N is the old producer's last block and the checkpoint hash is that block's
// hash. The address is the new producer's consensus address, which
// `qsdm-valkey show --in <key file>` prints. The tool reads the table with the
// node's own loader and checks the new schedule with the node's own rules. It
// writes a new file and never overwrites one. Then it loads the written file
// as the node will: the schedule must be exactly the new one, and every other
// setting must equal the input's, or the file is removed again. Install the
// same file on every node (section 5, steps 3 and 5).
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	"github.com/blackbeardONE/QSDM/pkg/config"
	"github.com/blackbeardONE/QSDM/pkg/producerpolicy"
)

const usage = `usage:
  qsdm-handover --config <core.toml> --out <new.toml> --from-height <N+1> --producer <address> --checkpoint-hash <hash of block N>

  N is the old producer's last block; <address> is the new producer's consensus
  address (qsdm-valkey show --in <key file>). The new file is checked by loading
  it as the node does; install it on every node (PRODUCER_TRANSITION.md section 5).
`

const transitionTable = "consensus.producer_transition"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("qsdm-handover", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	in := fs.String("config", "", "")
	out := fs.String("out", "", "")
	from := fs.Uint64("from-height", 0, "")
	producer := fs.String("producer", "", "")
	checkpoint := fs.String("checkpoint-hash", "", "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, usage)
			return 0
		}
		fmt.Fprintf(stderr, "qsdm-handover: %v\n%s", err, usage)
		return 2
	}
	if fs.NArg() != 0 || *in == "" || *out == "" || *from == 0 || *producer == "" || *checkpoint == "" {
		fmt.Fprint(stderr, "qsdm-handover: --config, --out, --from-height, --producer and --checkpoint-hash are all required\n"+usage)
		return 2
	}
	next, err := handover(*in, *out, *from, *producer, *checkpoint)
	if err != nil {
		fmt.Fprintf(stderr, "qsdm-handover: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "qsdm-handover: wrote %s\n", *out)
	fmt.Fprintln(stdout, "  producer schedule (version 2):")
	for _, e := range next.Epochs {
		if e.CheckpointHash == "" {
			fmt.Fprintf(stdout, "    from %d  %s\n", e.FromHeight, e.ProducerID)
		} else {
			fmt.Fprintf(stdout, "    from %d  %s  after block %d %s\n", e.FromHeight, e.ProducerID, e.FromHeight-1, e.CheckpointHash)
		}
	}
	fmt.Fprintf(stdout, "  checked: %s loads as the node loads it, with this schedule, and every other setting equals %s\n", *out, *in)
	fmt.Fprintln(stdout, "  next: install the same file on every node (PRODUCER_TRANSITION.md section 5, steps 3 and 5)")
	return 0
}

// handover writes out: the file in with its transition table replaced by the
// version 2 schedule that adds the epoch {from, producer, checkpoint}.
func handover(in, out string, from uint64, producer, checkpoint string) (*producerpolicy.Transition, error) {
	for _, p := range []string{in, out} {
		if strings.ToLower(filepath.Ext(p)) != ".toml" {
			return nil, fmt.Errorf("%s: only .toml files are written (the node chooses the format by the extension; write a YAML table by hand, PRODUCER_TRANSITION.md section 2)", p)
		}
	}
	cur, err := config.LoadConfigFile(in)
	if err != nil {
		return nil, err
	}
	old := cur.ProducerTransition
	if old == nil {
		return nil, fmt.Errorf("%s has no [%s] table: a first hand-over is a version 1 table (PRODUCER_TRANSITION.md section 1)", in, transitionTable)
	}
	schedule := old.Schedule()
	if last := schedule[len(schedule)-1]; from <= last.FromHeight {
		return nil, fmt.Errorf("--from-height %d is not above %d, where the current producer starts", from, last.FromHeight)
	}
	next := &producerpolicy.Transition{
		Version:                2,
		HistoricalPrefixBytes:  old.HistoricalPrefixBytes,
		HistoricalPrefixSHA256: old.HistoricalPrefixSHA256,
		Epochs:                 append(schedule, producerpolicy.Epoch{FromHeight: from, ProducerID: producer, CheckpointHash: checkpoint}),
	}
	if err := next.Validate(); err != nil {
		return nil, err
	}
	src, err := os.ReadFile(in)
	if err != nil {
		return nil, err
	}
	text, err := rewrite(string(src), tableTOML(next))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", in, err)
	}
	if err := writeNew(out, text); err != nil {
		return nil, err
	}
	got, err := config.LoadConfigFile(out)
	if err != nil {
		_ = os.Remove(out)
		return nil, fmt.Errorf("the written file does not load, so it was removed: %w", err)
	}
	if !reflect.DeepEqual(got.ProducerTransition, next) {
		_ = os.Remove(out)
		return nil, fmt.Errorf("the written file loads a different schedule (%+v), so it was removed", got.ProducerTransition)
	}
	a, b := *cur, *got
	a.ProducerTransition, b.ProducerTransition = nil, nil
	a.ConfigFileUsed, b.ConfigFileUsed = "", ""
	if !reflect.DeepEqual(a, b) {
		_ = os.Remove(out)
		return nil, errors.New("a setting outside the transition table loads differently from the written file, so it was removed")
	}
	return next, nil
}

// tableTOML is the version 2 table in the form of PRODUCER_TRANSITION.md
// section 2. Validate has already checked every value (decimal heights and
// lowercase hex), so no value needs escaping.
func tableTOML(p *producerpolicy.Transition) string {
	var b strings.Builder
	b.WriteString("[" + transitionTable + "]\n")
	b.WriteString("version = 2\n")
	fmt.Fprintf(&b, "historical_prefix_bytes = %d\n", p.HistoricalPrefixBytes)
	fmt.Fprintf(&b, "historical_prefix_sha256 = \"%s\"\n", p.HistoricalPrefixSHA256)
	for _, e := range p.Epochs {
		b.WriteString("\n[[" + transitionTable + ".epochs]]\n")
		fmt.Fprintf(&b, "from_height = %d\n", e.FromHeight)
		fmt.Fprintf(&b, "producer_id = \"%s\"\n", e.ProducerID)
		if e.CheckpointHash != "" {
			fmt.Fprintf(&b, "checkpoint_hash = \"%s\"\n", e.CheckpointHash)
		}
	}
	return b.String()
}

var (
	// headerRE matches a table header line, [a.b] or [[a.b]], with an
	// optional comment.
	headerRE = regexp.MustCompile(`^\s*\[(\[?)\s*([^\[\]#]+?)\s*\](\]?)\s*(#.*)?$`)
	// dottedRE matches a key that writes into the transition table without
	// its header: producer_transition = {...} or producer_transition.x = ...
	// under [consensus], consensus.producer_transition... at the top level.
	dottedRE = regexp.MustCompile(`^\s*(("?consensus"?)\s*\.\s*)?"?producer_transition"?\s*[.=]`)
)

// tableKey normalises a header's key: a."b" . c -> a.b.c.
func tableKey(raw string) string {
	parts := strings.Split(raw, ".")
	for i, p := range parts {
		parts[i] = strings.Trim(strings.TrimSpace(p), `"'`)
	}
	return strings.Join(parts, ".")
}

// rewrite replaces the transition table and its sub-tables (the epochs) with
// table, at the place of the first of them. Every other line is kept as it
// is; comments inside the old table go with it. A table written with inline
// or dotted keys is refused rather than guessed at.
func rewrite(src, table string) (string, error) {
	if strings.Contains(src, "\r\n") {
		table = strings.ReplaceAll(table, "\n", "\r\n")
	}
	var out strings.Builder
	current := "" // the table the line belongs to ("" is the top level)
	inserted, dropping := false, false
	for _, line := range strings.SplitAfter(src, "\n") {
		if m := headerRE.FindStringSubmatch(strings.TrimRight(line, "\r\n")); m != nil && (m[1] == "") == (m[3] == "") {
			current = tableKey(m[2])
			if current == transitionTable || strings.HasPrefix(current, transitionTable+".") {
				if !inserted {
					out.WriteString(table)
					inserted = true
				}
				dropping = true
				continue
			}
			if dropping {
				if strings.HasSuffix(table, "\r\n") {
					out.WriteString("\r\n")
				} else {
					out.WriteString("\n")
				}
			}
			dropping = false
		}
		if dropping {
			continue
		}
		if (current == "" || current == "consensus") && dottedRE.MatchString(line) {
			return "", errors.New("the transition table is written with inline or dotted keys; write it as the [consensus.producer_transition] table by hand (PRODUCER_TRANSITION.md section 2)")
		}
		out.WriteString(line)
	}
	if !inserted {
		return "", fmt.Errorf("no [%s] table header found", transitionTable)
	}
	return out.String(), nil
}

// writeNew creates path (never overwriting) with mode 0600 and syncs it. On
// any error after the create, the partial file is removed.
func writeNew(path, text string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s already exists; choose a new --out", path)
		}
		return err
	}
	_, werr := f.WriteString(text)
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(path)
		return werr
	}
	return nil
}
