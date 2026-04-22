package contracts

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ViewResult is the result of a read-only contract call.
type ViewResult struct {
	ContractID   string      `json:"contract_id"`
	FunctionName string      `json:"function_name"`
	Output       interface{} `json:"output"`
	GasEstimate  int64       `json:"gas_estimate"`
	Timestamp    time.Time   `json:"timestamp"`
	Cached       bool        `json:"cached"`
}

// ViewCallOption controls view call behaviour.
type ViewCallOption func(*viewCallOpts)

type viewCallOpts struct {
	gasLimit int64
	cache    bool
}

// WithGasEstimate sets the gas budget for the view call (for estimation only).
func WithGasEstimate(limit int64) ViewCallOption {
	return func(o *viewCallOpts) { o.gasLimit = limit }
}

// QueryContract executes a read-only call that does NOT modify contract state,
// does NOT consume gas from the caller, and does NOT emit events.
// Only functions where ABI declares StateMutating=false are allowed through this path.
func (ce *ContractEngine) QueryContract(ctx context.Context, contractID, functionName string, args map[string]interface{}, opts ...ViewCallOption) (*ViewResult, error) {
	ce.mu.RLock()
	contract, exists := ce.contracts[contractID]
	ce.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("contract %s not found", contractID)
	}

	fn, err := ce.findFunction(contract, functionName)
	if err != nil {
		return nil, err
	}

	if fn.StateMutating {
		return nil, fmt.Errorf("function %s is state-mutating; use ExecuteContract instead", functionName)
	}

	opt := viewCallOpts{gasLimit: DefaultGasLimit}
	for _, o := range opts {
		o(&opt)
	}

	// Snapshot contract state so we can restore it after the call
	stateCopy := snapshotState(contract.State)

	meter := NewGasMeter(opt.gasLimit)

	result, execErr := ce.executeReadOnly(ctx, contract, fn, args, meter)

	// Always restore state (view calls must not have side effects)
	contract.State = stateCopy

	if execErr != nil {
		return nil, fmt.Errorf("view call %s.%s failed: %w", contractID, functionName, execErr)
	}

	return &ViewResult{
		ContractID:   contractID,
		FunctionName: functionName,
		Output:       result,
		GasEstimate:  meter.Consumed(),
		Timestamp:    time.Now(),
	}, nil
}

// IsViewFunction checks whether a function is a read-only view function.
func (ce *ContractEngine) IsViewFunction(contractID, functionName string) (bool, error) {
	ce.mu.RLock()
	contract, exists := ce.contracts[contractID]
	ce.mu.RUnlock()

	if !exists {
		return false, fmt.Errorf("contract %s not found", contractID)
	}

	fn, err := ce.findFunction(contract, functionName)
	if err != nil {
		return false, err
	}
	return !fn.StateMutating, nil
}

// ListViewFunctions returns all non-state-mutating functions for a contract.
func (ce *ContractEngine) ListViewFunctions(contractID string) ([]Function, error) {
	ce.mu.RLock()
	contract, exists := ce.contracts[contractID]
	ce.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("contract %s not found", contractID)
	}

	if contract.ABI == nil {
		return nil, nil
	}

	var views []Function
	for _, fn := range contract.ABI.Functions {
		if !fn.StateMutating {
			views = append(views, fn)
		}
	}
	return views, nil
}

// EstimateGas runs a read-only execution to estimate gas cost without side effects.
func (ce *ContractEngine) EstimateGas(ctx context.Context, contractID, functionName string, args map[string]interface{}) (int64, error) {
	vr, err := ce.QueryContract(ctx, contractID, functionName, args)
	if err != nil {
		return 0, err
	}
	return vr.GasEstimate, nil
}

// executeReadOnly runs the contract execution without modifying persistent state.
// Uses the same execution pipeline as executeWASMMetered but without event emission.
func (ce *ContractEngine) executeReadOnly(ctx context.Context, contract *Contract, fn *Function, args map[string]interface{}, meter *GasMeter) (interface{}, error) {
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("marshal arguments: %w", err)
	}

	if err := meter.Consume(int64(len(fmt.Sprint(args))) * GasPerByteInput); err != nil {
		return nil, err
	}

	// Try per-contract wazero (V2 stateful modules)
	if rt, ok := ce.contractRTs[contract.ID]; ok && rt.HasFunction(fn.Name) {
		if err := meter.Consume(GasPerWASMCall); err != nil {
			return nil, err
		}
		result, err := rt.Call(fn.Name, argsJSON)
		if err == nil {
			return result, nil
		}
	}

	// Try shared wazero
	if ce.wazeroRT != nil && ce.wazeroRT.HasFunction(fn.Name) {
		if err := meter.Consume(GasPerWASMCall); err != nil {
			return nil, err
		}
		result, err := ce.wazeroRT.Call(fn.Name, argsJSON)
		if err != nil {
			return nil, err
		}
		return result, nil
	}

	// Try wasmer SDK
	if ce.wasmSDK != nil {
		if err := meter.Consume(GasPerWASMCall); err != nil {
			return nil, err
		}
		result, err := ce.wasmSDK.CallFunction(fn.Name, string(argsJSON))
		if err != nil {
			return nil, err
		}
		return result, nil
	}

	// Fallback simulation (no events emitted for read-only)
	if err := meter.Consume(GasPerSimOp); err != nil {
		return nil, err
	}
	return ce.simulateReadOnly(contract, fn, args)
}

// simulateReadOnly handles the simulation fallback for view calls.
// Only pure-read operations return data; state-mutating sims return an error.
func (ce *ContractEngine) simulateReadOnly(contract *Contract, fn *Function, args map[string]interface{}) (interface{}, error) {
	switch fn.Name {
	case "balanceOf":
		addr, _ := args["address"].(string)
		bal, _ := toFloat64(contract.State["balance:"+addr])
		return map[string]interface{}{"balance": bal}, nil

	case "getResults":
		proposal, _ := args["proposal"].(string)
		yes, _ := toFloat64(contract.State["yes:"+proposal])
		no, _ := toFloat64(contract.State["no:"+proposal])
		return map[string]interface{}{"yes": yes, "no": no}, nil

	default:
		return map[string]interface{}{
			"function": fn.Name,
			"args":     args,
			"contract": contract.ID,
			"mode":     "view_simulated",
		}, nil
	}
}

func (ce *ContractEngine) findFunction(contract *Contract, name string) (*Function, error) {
	if contract.ABI == nil {
		return nil, fmt.Errorf("contract %s has no ABI", contract.ID)
	}
	for i := range contract.ABI.Functions {
		if contract.ABI.Functions[i].Name == name {
			return &contract.ABI.Functions[i], nil
		}
	}
	return nil, fmt.Errorf("function %s not found in contract %s", name, contract.ID)
}

func snapshotState(state map[string]interface{}) map[string]interface{} {
	if state == nil {
		return nil
	}
	data, _ := json.Marshal(state)
	var cp map[string]interface{}
	json.Unmarshal(data, &cp)
	return cp
}
