package governancecli

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/governance"
)

// GovernanceCLI runs the governance command line interface.
func GovernanceCLI(sv *governance.SnapshotVoting) {
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
			if len(args) < 5 {
				fmt.Println("Usage: propose <proposalID> <durationSeconds> <quorum> <description>")
				continue
			}
			id := args[1]
			durationSeconds, err := strconv.Atoi(args[2])
			if err != nil {
				fmt.Println("Invalid duration:", err)
				continue
			}
			quorum, err := strconv.Atoi(args[3])
			if err != nil {
				fmt.Println("Invalid quorum:", err)
				continue
			}
			description := strings.Join(args[4:], " ")
			err = sv.AddProposal(id, description, time.Duration(durationSeconds)*time.Second, quorum)
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
			weight, err := strconv.Atoi(args[3])
			if err != nil {
				fmt.Println("Invalid weight:", err)
				continue
			}
			support := false
			if args[4] == "true" {
				support = true
			}
			err = sv.Vote(proposalID, voterID, weight, support)
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
					fmt.Printf("- %s: %s (For: %d, Against: %d, Finalized: %v, ExpiresAt: %v, Quorum: %d)\n",
						id, p.Description, p.VotesFor, p.VotesAgainst, p.Finalized, p.ExpiresAt, p.Quorum)
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
