package contracts

import "fmt"

// ContractTemplate represents a contract template
type ContractTemplate struct {
	Name        string
	Description string
	Code        []byte
	ABI         *ABI
}

// GetTemplates returns available contract templates
func GetTemplates() []ContractTemplate {
	return []ContractTemplate{
		{
			Name:        "SimpleToken",
			Description: "A simple ERC-20-like token contract",
			Code:        tokenV2WASM,
			ABI: &ABI{
				Functions: []Function{
					{
						Name:       "transfer",
						Inputs:     []Param{{Name: "from", Type: "uint32"}, {Name: "to", Type: "uint32"}, {Name: "amount", Type: "uint32"}},
						Outputs:    []Param{{Name: "success", Type: "uint32"}},
						Payable:    false,
						StateMutating: true,
					},
					{
						Name:       "set_balance",
						Inputs:     []Param{{Name: "slot", Type: "uint32"}, {Name: "amount", Type: "uint32"}},
						Outputs:    nil,
						Payable:    false,
						StateMutating: true,
					},
					{
						Name:       "get_balance",
						Inputs:     []Param{{Name: "slot", Type: "uint32"}},
						Outputs:    []Param{{Name: "balance", Type: "uint32"}},
						Payable:    false,
						StateMutating: false,
					},
					{
						Name:       "add",
						Inputs:     []Param{{Name: "a", Type: "uint32"}, {Name: "b", Type: "uint32"}},
						Outputs:    []Param{{Name: "result", Type: "uint32"}},
						Payable:    false,
						StateMutating: false,
					},
					{
						Name:       "balanceOf",
						Inputs:     []Param{{Name: "address", Type: "address"}},
						Outputs:    []Param{{Name: "balance", Type: "uint256"}},
						Payable:    false,
						StateMutating: false,
					},
				},
				Events: []Event{
					{
						Name:   "Transfer",
						Params: []Param{{Name: "from", Type: "address"}, {Name: "to", Type: "address"}, {Name: "amount", Type: "uint256"}},
					},
				},
			},
		},
		{
			Name:        "Voting",
			Description: "A simple voting contract",
			Code:        votingV2WASM,
			ABI: &ABI{
				Functions: []Function{
					{
						Name:       "vote_yes",
						Inputs:     []Param{{Name: "proposal_slot", Type: "uint32"}},
						Outputs:    []Param{{Name: "new_count", Type: "uint32"}},
						Payable:    false,
						StateMutating: true,
					},
					{
						Name:       "vote_no",
						Inputs:     []Param{{Name: "proposal_slot", Type: "uint32"}},
						Outputs:    []Param{{Name: "new_count", Type: "uint32"}},
						Payable:    false,
						StateMutating: true,
					},
					{
						Name:       "get_yes",
						Inputs:     []Param{{Name: "proposal_slot", Type: "uint32"}},
						Outputs:    []Param{{Name: "count", Type: "uint32"}},
						Payable:    false,
						StateMutating: false,
					},
					{
						Name:       "get_no",
						Inputs:     []Param{{Name: "proposal_slot", Type: "uint32"}},
						Outputs:    []Param{{Name: "count", Type: "uint32"}},
						Payable:    false,
						StateMutating: false,
					},
					{
						Name:       "increment",
						Inputs:     []Param{{Name: "value", Type: "uint32"}},
						Outputs:    []Param{{Name: "result", Type: "uint32"}},
						Payable:    false,
						StateMutating: false,
					},
					{
						Name:       "vote",
						Inputs:     []Param{{Name: "proposal", Type: "string"}, {Name: "choice", Type: "bool"}},
						Outputs:    []Param{{Name: "success", Type: "bool"}},
						Payable:    false,
						StateMutating: true,
					},
					{
						Name:       "getResults",
						Inputs:     []Param{{Name: "proposal", Type: "string"}},
						Outputs:    []Param{{Name: "yes", Type: "uint256"}, {Name: "no", Type: "uint256"}},
						Payable:    false,
						StateMutating: false,
					},
				},
				Events: []Event{
					{
						Name:   "VoteCast",
						Params: []Param{{Name: "proposal", Type: "string"}, {Name: "voter", Type: "address"}, {Name: "choice", Type: "bool"}},
					},
				},
			},
		},
		{
			Name:        "Escrow",
			Description: "An escrow contract for secure transactions",
			Code:        escrowV2WASM,
			ABI: &ABI{
				Functions: []Function{
					{
						Name:       "deposit",
						Inputs:     []Param{{Name: "slot", Type: "uint32"}, {Name: "amount", Type: "uint32"}},
						Outputs:    []Param{{Name: "slot", Type: "uint32"}},
						Payable:    true,
						StateMutating: true,
					},
					{
						Name:       "release",
						Inputs:     []Param{{Name: "slot", Type: "uint32"}},
						Outputs:    []Param{{Name: "success", Type: "uint32"}},
						Payable:    false,
						StateMutating: true,
					},
					{
						Name:       "refund",
						Inputs:     []Param{{Name: "slot", Type: "uint32"}},
						Outputs:    []Param{{Name: "success", Type: "uint32"}},
						Payable:    false,
						StateMutating: true,
					},
					{
						Name:       "get_status",
						Inputs:     []Param{{Name: "slot", Type: "uint32"}},
						Outputs:    []Param{{Name: "status", Type: "uint32"}},
						Payable:    false,
						StateMutating: false,
					},
					{
						Name:       "get_amount",
						Inputs:     []Param{{Name: "slot", Type: "uint32"}},
						Outputs:    []Param{{Name: "amount", Type: "uint32"}},
						Payable:    false,
						StateMutating: false,
					},
				},
				Events: []Event{
					{
						Name:   "EscrowCreated",
						Params: []Param{{Name: "escrowId", Type: "string"}, {Name: "amount", Type: "uint256"}},
					},
					{
						Name:   "EscrowReleased",
						Params: []Param{{Name: "escrowId", Type: "string"}},
					},
				},
			},
		},
	}
}

// GetTemplate returns a contract template by name
func GetTemplate(name string) (*ContractTemplate, error) {
	templates := GetTemplates()
	for i := range templates {
		if templates[i].Name == name {
			return &templates[i], nil
		}
	}
	return nil, fmt.Errorf("template %s not found", name)
}

