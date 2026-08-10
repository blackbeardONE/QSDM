package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

// stake-helper: validator bonding for qsdm/staking/v1.
//
// Two surfaces:
//
//   - delegate / unbond emit a validated payload for operators who sign and
//     submit through their own tooling (mirrors gov-helper). Useful because
//     `qsdmcli tx` builds only plain transfers, and wallet.TransactionData —
//     the envelope /wallet/submit-signed accepts — has no ContractID or
//     Payload field, so a contract tx cannot travel over it.
//   - submit-delegate / submit-unbond sign with the local ML-DSA-87 keystore
//     and POST to /api/v1/staking/submit-signed. See stake_submit.go.
//
// Payloads are round-tripped through the same decoder the chain applier
// uses, so anything printed or submitted here is accepted at apply time
// rather than failing after it has already cost a block.

func (c *CLI) stakeHelper(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: qsdmcli stake-helper <delegate|unbond|show|submit-delegate|submit-unbond> [flags]")
	}
	switch args[0] {
	case "delegate":
		return c.stakeHelperBuild(chain.StakingActionDelegate, args[1:])
	case "unbond", "begin-unbond":
		return c.stakeHelperBuild(chain.StakingActionUnbond, args[1:])
	case "show":
		return c.stakeHelperShow(args[1:])
	case "submit-delegate":
		return c.stakeHelperSubmit(chain.StakingActionDelegate, args[1:])
	case "submit-unbond":
		return c.stakeHelperSubmit(chain.StakingActionUnbond, args[1:])
	default:
		return fmt.Errorf(
			"unknown stake-helper subcommand %q (want delegate|unbond|show|submit-delegate|submit-unbond)",
			args[0])
	}
}

func (c *CLI) stakeHelperBuild(action string, args []string) error {
	name := "stake-helper " + action
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		validator = fs.String("validator", "", "validator address to bond to (required)")
		amount    = fs.Float64("amount", 0, "amount of CELL to bond (required, > 0)")
		unbondFor = fs.Uint64("unbond-blocks", 0,
			"blocks before unbonded funds mature (unbond only; 0 uses the protocol default)")
		out = fs.String("out", "-", "output path for the encoded payload ('-' for stdout)")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*validator) == "" {
		fs.Usage()
		return errors.New("--validator is required")
	}
	if *amount <= 0 {
		fs.Usage()
		return errors.New("--amount is required and must be positive")
	}

	payload := chain.StakingPayload{
		Action:    action,
		Validator: strings.TrimSpace(*validator),
		Amount:    *amount,
	}
	if action == chain.StakingActionUnbond {
		payload.UnbondBlocks = *unbondFor
	}

	// Validate through the same decoder the chain applier uses, so a
	// payload this command prints cannot be rejected at apply time.
	encoded, err := chain.EncodeStakingPayload(payload)
	if err != nil {
		return fmt.Errorf("payload rejected by the chain validator: %w", err)
	}

	if *out == "-" {
		fmt.Println(string(encoded))
	} else if err := writeBytes(resolveOutPath(*out), encoded); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr,
		"\nSubmit as a %s transaction from the bonding account.\n"+
			"The delegator is taken from the transaction SENDER, not this payload,\n"+
			"so it can only ever bond the signer's own funds.\n"+
			"Leave the transaction Amount at 0: the applier debits the account itself,\n"+
			"and a non-zero Amount is refused to avoid moving funds twice.\n",
		chain.StakingContractID)

	if action == chain.StakingActionDelegate {
		fmt.Fprintf(os.Stderr,
			"\nBonding at or above the %.0f CELL minimum makes this address a validator\n"+
				"derived from chain state, so every node computes the same set.\n",
			chain.DefaultValidatorSetConfig().MinStake)
	} else {
		blocks := *unbondFor
		if blocks == 0 {
			blocks = chain.DefaultUnbondBlocks
		}
		fmt.Fprintf(os.Stderr,
			"\nVoting power drops immediately; funds mature after %d blocks.\n", blocks)
	}
	return nil
}

// stakeHelperShow reports current bonded stake and the resulting derived
// validator membership, so an operator can see whether their bond took
// effect without reading logs.
func (c *CLI) stakeHelperShow(args []string) error {
	fs := flag.NewFlagSet("stake-helper show", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}

	body, err := c.get("/validators")
	if err != nil {
		return fmt.Errorf("fetch validators: %w", err)
	}
	var pretty interface{}
	if err := json.Unmarshal(body, &pretty); err != nil {
		fmt.Println(string(body))
		return nil
	}
	prettyPrint(body)
	fmt.Fprintf(os.Stderr,
		"\nMembership is derived from bonded stake (minimum %.0f CELL).\n"+
			"An address below the minimum is not in the set regardless of what it holds.\n",
		chain.DefaultValidatorSetConfig().MinStake)
	return nil
}
