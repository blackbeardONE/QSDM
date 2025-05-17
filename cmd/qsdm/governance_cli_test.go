package main

import (
    "bytes"
    "strings"
    "testing"

    "github.com/blackbeardONE/QSDM/pkg/governance"
)

func TestGovernanceCLI(t *testing.T) {
    sv := governance.NewSnapshotVoting()

    input := "propose prop1 Increase block size\n" +
        "vote prop1 voter1 5 true\n" +
        "vote prop1 voter2 3 false\n" +
        "finalize prop1\n" +
        "list\n" +
        "exit\n"

    // Add voters with token weights
    sv.Voters["voter1"] = 5
    sv.Voters["voter2"] = 3

    // Redirect stdin and stdout
    oldStdin := stdin
    oldStdout := stdout
    defer func() {
        stdin = oldStdin
        stdout = oldStdout
    }()

    stdin = strings.NewReader(input)
    var buf bytes.Buffer
    stdout = &buf

    governanceCLI(sv)

    output := buf.String()
    if !strings.Contains(output, "Proposal added: prop1") {
        t.Errorf("Expected proposal added message, got %s", output)
    }
    if !strings.Contains(output, "Vote cast for proposal: prop1") {
        t.Errorf("Expected vote cast message, got %s", output)
    }
    if !strings.Contains(output, "Proposal passed: prop1") && !strings.Contains(output, "Proposal failed: prop1") {
        t.Errorf("Expected proposal finalized message, got %s", output)
    }
    if !strings.Contains(output, "Proposals:") {
        t.Errorf("Expected proposals list, got %s", output)
    }
}
