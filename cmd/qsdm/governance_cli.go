package main

import (
    "bufio"
    "fmt"
    "os"
    "strings"

    "github.com/blackbeardONE/QSDM/pkg/governance"
)

func governanceCLI(sv *governance.SnapshotVoting) {
    reader := bufio.NewReader(os.Stdin)
    fmt.Println("Governance CLI")
    fmt.Println("--------------")

    for {
        fmt.Print("Enter command (propose, vote, finalize, list, exit): ")
        input, _ := reader.ReadString('\n')
        input = strings.TrimSpace(input)
        args := strings.Split(input, " ")

        switch args[0] {
        case "propose":
            if len(args) < 3 {
                fmt.Println("Usage: propose <proposalID> <description>")
                continue
            }
            id := args[1]
            description := strings.Join(args[2:], " ")
            err := sv.AddProposal(id, description)
            if err != nil {
                fmt.Println("Error:", err)
            } else {
                fmt.Println("Proposal added:", id)
            }
        case "vote":
            if len(args) < 5 {
                fmt.Println("Usage: vote <proposalID> <voterID> <weight> <support(true|false)>")
                continue
            }
            proposalID := args[1]
            voterID := args[2]
            weight := 0
            fmt.Sscanf(args[3], "%d", &weight)
            support := false
            if args[4] == "true" {
                support = true
            }
            err := sv.Vote(proposalID, voterID, weight, support)
            if err != nil {
                fmt.Println("Error:", err)
            } else {
                fmt.Println("Vote cast for proposal:", proposalID)
            }
        case "finalize":
            if len(args) < 2 {
                fmt.Println("Usage: finalize <proposalID>")
                continue
            }
            proposalID := args[1]
            passed, err := sv.FinalizeProposal(proposalID)
            if err != nil {
                fmt.Println("Error:", err)
            } else if passed {
                fmt.Println("Proposal passed:", proposalID)
            } else {
                fmt.Println("Proposal failed:", proposalID)
            }
        case "list":
            sv.Mu.RLock()
            if len(sv.Proposals) == 0 {
                fmt.Println("No proposals found.")
            } else {
                fmt.Println("Proposals:")
                for id, p := range sv.Proposals {
                    fmt.Printf("- %s: %s (For: %d, Against: %d, Finalized: %v)\n", id, p.Description, p.VotesFor, p.VotesAgainst, p.Finalized)
                }
            }
            sv.Mu.RUnlock()
        case "exit":
            fmt.Println("Exiting Governance CLI.")
            return
        default:
            fmt.Println("Unknown command:", args[0])
        }
    }
}
