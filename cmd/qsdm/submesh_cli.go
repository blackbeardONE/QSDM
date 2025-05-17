package main

import (
    "bufio"
    "fmt"
    "os"
    "strconv"
    "strings"

    "github.com/blackbeardONE/QSDM/pkg/submesh"
)

func submeshCLI(manager *submesh.DynamicSubmeshManager) {
    reader := bufio.NewReader(os.Stdin)
    fmt.Println("Dynamic Submesh CLI")
    fmt.Println("-------------------")

    for {
        fmt.Print("Enter command (add, update, remove, list, apply, exit): ")
        input, _ := reader.ReadString('\n')
        input = strings.TrimSpace(input)
        args := strings.Split(input, " ")

        switch args[0] {
        case "add", "update":
            if len(args) < 5 {
                fmt.Println("Usage: add|update <name> <feeThreshold> <priorityLevel> <geoTags(comma-separated)>")
                continue
            }
            fee, err := strconv.ParseFloat(args[2], 64)
            if err != nil {
                fmt.Println("Invalid feeThreshold:", err)
                continue
            }
            priority, err := strconv.Atoi(args[3])
            if err != nil {
                fmt.Println("Invalid priorityLevel:", err)
                continue
            }
            geoTags := strings.Split(args[4], ",")
            ds := &submesh.DynamicSubmesh{
                Name:          args[1],
                FeeThreshold:  fee,
                PriorityLevel: priority,
                GeoTags:       geoTags,
            }
            manager.AddOrUpdateSubmesh(ds)
            fmt.Printf("Submesh %s added/updated.\n", ds.Name)
        case "remove":
            if len(args) < 2 {
                fmt.Println("Usage: remove <name>")
                continue
            }
            err := manager.RemoveSubmesh(args[1])
            if err != nil {
                fmt.Println("Error:", err)
            } else {
                fmt.Printf("Submesh %s removed.\n", args[1])
            }
        case "list":
            manager.Mu.RLock()
            if len(manager.Submeshes) == 0 {
                fmt.Println("No submeshes defined.")
            } else {
                fmt.Println("Defined submeshes:")
                for _, ds := range manager.Submeshes {
                    fmt.Printf("- %s: FeeThreshold=%.4f, Priority=%d, GeoTags=%v\n", ds.Name, ds.FeeThreshold, ds.PriorityLevel, ds.GeoTags)
                }
            }
            manager.Mu.RUnlock()
        case "apply":
            // For demonstration, apply a hardcoded governance update
            updates := []*submesh.DynamicSubmesh{
                {
                    Name:          "fastlane",
                    FeeThreshold:  0.02,
                    PriorityLevel: 15,
                    GeoTags:       []string{"US", "EU"},
                },
                {
                    Name:          "slowlane",
                    FeeThreshold:  0.001,
                    PriorityLevel: 1,
                    GeoTags:       []string{"US"},
                },
            }
            manager.ApplyGovernanceUpdate(updates)
            fmt.Println("Applied governance updates to submeshes.")
        case "exit":
            fmt.Println("Exiting CLI.")
            return
        default:
            fmt.Println("Unknown command:", args[0])
        }
    }
}
