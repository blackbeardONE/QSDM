package main

import (
    "bufio"
    "fmt"
    "os"
    "strings"
    "time"
    "github.com/blackbeardONE/QSDM/pkg/governance"
)

func governanceCLI() {
    reader := bufio.NewReader(os.Stdin)
    fmt.Println("Governance Voting CLI")
    fmt.Println("---------------------")

    // Create a snapshot with 5 minutes expiry for demo
    snapshot := governance.NewSnapshot("snapshot1", 5*time.Minute)

    // For demo, set some token weights
    snapshot.SetTokenWeight("voter1", 100)
    snapshot.SetTokenWeight("voter2", 50)
    snapshot.SetTokenWeight("voter3", 25)

    for {
        fmt.Print("Enter command (vote <voterID> <yes|no|abstain>, result, exit): ")
        input, _ := reader.ReadString('\n')
        input = strings.TrimSpace(input)
        args := strings.Split(input, " ")

        if len(args) == 0 {
            continue
        }

        switch args[0] {
        case "vote":
            if len(args) < 3 {
                fmt.Println("Usage: vote <voterID> <yes|no|abstain>")
                continue
            }
            voterID := args[1]
            var option governance.VoteOption
            switch strings.ToLower(args[2]) {
            case "yes":
                option = governance.VoteYes
            case "no":
                option = governance.VoteNo
            case "abstain":
                option = governance.VoteAbstain
            default:
                fmt.Println("Invalid vote option. Use yes, no, or abstain.")
                continue
            }
            err := snapshot.CastVote(voterID, option)
            if err != nil {
                fmt.Println("Error casting vote:", err)
            } else {
                fmt.Printf("Vote cast by %s: %s\n", voterID, args[2])
            }
        case "result":
            fmt.Println("Current voting results:")
            fmt.Println(snapshot.Result())
        case "exit":
            fmt.Println("Exiting governance CLI.")
            return
        default:
            fmt.Println("Unknown command:", args[0])
        }
    }
}
