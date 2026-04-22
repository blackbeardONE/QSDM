package contracts

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/wasm"
)

// ContractEngine executes smart contracts using WASM
type ContractEngine struct {
	wasmSDK      *wasm.WASMSDK
	wazeroRT     *wasm.WazeroRuntime
	contractRTs  map[string]*wasm.WazeroRuntime // per-contract wazero runtimes (isolated memory)
	contracts    map[string]*Contract
	mu           sync.RWMutex
	executions   map[string]*ExecutionResult
	gasConfig    GasConfig
	Events       *EventIndex
	tracer       *CallTracer
}

// Contract represents a smart contract
type Contract struct {
	ID             string
	Code           []byte
	ABI            *ABI
	DeployedAt     time.Time
	Owner          string
	State          map[string]interface{}
	GasUsedDeploy  int64
	TotalGasUsed   int64 // cumulative gas across all executions
}

// ABI represents the Application Binary Interface of a contract
type ABI struct {
	Functions []Function `json:"functions"`
	Events    []Event    `json:"events"`
}

// Function represents a contract function
type Function struct {
	Name       string   `json:"name"`
	Inputs     []Param  `json:"inputs"`
	Outputs    []Param  `json:"outputs"`
	Payable    bool     `json:"payable"`
	StateMutating bool  `json:"stateMutating"`
}

// Param represents a function parameter
type Param struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// Event represents a contract event
type Event struct {
	Name   string  `json:"name"`
	Params []Param `json:"params"`
}

// ExecutionResult represents the result of contract execution
type ExecutionResult struct {
	Success   bool
	Output    interface{}
	GasUsed   int64
	Error     string
	Timestamp time.Time
}

// NewContractEngine creates a new contract execution engine
func NewContractEngine(wasmSDK *wasm.WASMSDK) *ContractEngine {
	return &ContractEngine{
		wasmSDK:     wasmSDK,
		contractRTs: make(map[string]*wasm.WazeroRuntime),
		contracts:   make(map[string]*Contract),
		executions:  make(map[string]*ExecutionResult),
		gasConfig:   DefaultGasConfig(),
		Events:      NewEventIndex(10000),
		tracer:      NewCallTracer(10000),
	}
}

// SetGasConfig overrides the default gas configuration.
func (ce *ContractEngine) SetGasConfig(cfg GasConfig) { ce.gasConfig = cfg }

// SetTracer overrides the default call tracer.
func (ce *ContractEngine) SetTracer(tr *CallTracer) {
	ce.mu.Lock()
	defer ce.mu.Unlock()
	ce.tracer = tr
}

// Tracer returns the active call tracer.
func (ce *ContractEngine) Tracer() *CallTracer {
	ce.mu.RLock()
	defer ce.mu.RUnlock()
	return ce.tracer
}

// SetWazeroRuntime attaches a wazero-based WASM runtime (pure Go, no CGO needed).
func (ce *ContractEngine) SetWazeroRuntime(rt *wasm.WazeroRuntime) {
	ce.wazeroRT = rt
}

// DeployContract deploys a new smart contract
func (ce *ContractEngine) DeployContract(ctx context.Context, contractID string, code []byte, abi *ABI, owner string) (*Contract, error) {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	if _, exists := ce.contracts[contractID]; exists {
		return nil, fmt.Errorf("contract %s already exists", contractID)
	}

	contract := &Contract{
		ID:         contractID,
		Code:       code,
		ABI:        abi,
		DeployedAt: time.Now(),
		Owner:      owner,
		State:      make(map[string]interface{}),
	}

	contract.GasUsedDeploy = DeploymentGas(len(code))
	ce.contracts[contractID] = contract

	// Try to instantiate a per-contract wazero runtime for stateful WASM execution
	if len(code) > 4 && code[0] == 0x00 && code[1] == 0x61 && code[2] == 0x73 && code[3] == 0x6d {
		rt, rtErr := wasm.NewWazeroRuntime(code)
		if rtErr == nil {
			ce.contractRTs[contractID] = rt
		}
	}

	return contract, nil
}

// ExecuteContract executes a contract function
func (ce *ContractEngine) ExecuteContract(ctx context.Context, contractID string, functionName string, args map[string]interface{}) (*ExecutionResult, error) {
	ce.mu.RLock()
	contract, exists := ce.contracts[contractID]
	ce.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("contract %s not found", contractID)
	}

	// Find function in ABI
	var fn *Function
	for i := range contract.ABI.Functions {
		if contract.ABI.Functions[i].Name == functionName {
			fn = &contract.ABI.Functions[i]
			break
		}
	}

	if fn == nil {
		return nil, fmt.Errorf("function %s not found in contract", functionName)
	}

	var tb *TraceBuilder
	traceID := fmt.Sprintf("%s_%s_%d", contractID, functionName, time.Now().UnixNano())
	ce.mu.RLock()
	localTracer := ce.tracer
	ce.mu.RUnlock()
	if localTracer != nil {
		tb = localTracer.BeginTrace(traceID, contractID, functionName, "", args)
	}

	gasLimit := ce.gasConfig.DefaultLimit
	if gl, ok := args["_gas_limit"]; ok {
		if glf, okf := toFloat64(gl); okf && int64(glf) > 0 {
			gasLimit = int64(glf)
		}
		delete(args, "_gas_limit")
	}
	if gasLimit > ce.gasConfig.MaxLimit {
		gasLimit = ce.gasConfig.MaxLimit
	}
	meter := NewGasMeter(gasLimit)

	// Charge per-byte input cost
	beforeInput := meter.Consumed()
	if err := meter.Consume(int64(len(fmt.Sprint(args))) * GasPerByteInput); err != nil {
		if tb != nil {
			tb.RecordOp("input_charge", beforeInput, meter.Consumed(), map[string]interface{}{"args_len": len(fmt.Sprint(args))}, err)
			localTracer.Finish(tb, meter.Consumed(), nil, err)
		}
		return &ExecutionResult{Success: false, Error: err.Error(), GasUsed: meter.Consumed(), Timestamp: time.Now()}, err
	}
	if tb != nil {
		tb.RecordOp("input_charge", beforeInput, meter.Consumed(), map[string]interface{}{"args_len": len(fmt.Sprint(args))}, nil)
	}

	beforeExec := meter.Consumed()
	result, err := ce.executeWASMMetered(ctx, contract, fn, args, meter)
	if tb != nil {
		tb.RecordOp("execute_pipeline", beforeExec, meter.Consumed(), map[string]interface{}{"function": functionName}, err)
	}

	execResult := &ExecutionResult{
		Success:   err == nil,
		Output:    result,
		GasUsed:   meter.Consumed(),
		Error:     func() string { if err != nil { return err.Error() }; return "" }(),
		Timestamp: time.Now(),
	}

	// Store execution result and update cumulative gas
	ce.mu.Lock()
	executionID := fmt.Sprintf("%s_%s_%d", contractID, functionName, time.Now().UnixNano())
	ce.executions[executionID] = execResult
	if c, ok := ce.contracts[contractID]; ok {
		c.TotalGasUsed += meter.Consumed()
	}
	ce.mu.Unlock()

	if tb != nil {
		localTracer.Finish(tb, meter.Consumed(), result, err)
	}

	return execResult, err
}

// executeWASMMetered executes contract code with gas metering.
// Priority: per-contract wazero > shared wazero > wasmSDK > simulation.
func (ce *ContractEngine) executeWASMMetered(ctx context.Context, contract *Contract, fn *Function, args map[string]interface{}, meter *GasMeter) (interface{}, error) {
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal arguments: %w", err)
	}

	// 1a. Try per-contract wazero runtime (V2 modules with isolated memory).
	if rt, ok := ce.contractRTs[contract.ID]; ok && rt.HasFunction(fn.Name) {
		if err := meter.Consume(GasPerWASMCall); err != nil {
			return nil, err
		}
		result, err := rt.Call(fn.Name, argsJSON)
		if err == nil {
			return result, nil
		}
	}

	// 1b. Try shared wazero runtime (legacy single-module)
	if ce.wazeroRT != nil && ce.wazeroRT.HasFunction(fn.Name) {
		if err := meter.Consume(GasPerWASMCall); err != nil {
			return nil, err
		}
		result, err := ce.wazeroRT.Call(fn.Name, argsJSON)
		if err != nil {
			return nil, fmt.Errorf("wazero execution failed for %s.%s: %w", contract.ID, fn.Name, err)
		}
		return result, nil
	}

	// 2. Try legacy wasmer SDK (requires CGO + DLLs)
	if ce.wasmSDK != nil {
		if err := meter.Consume(GasPerWASMCall); err != nil {
			return nil, err
		}
		result, err := ce.wasmSDK.CallFunction(fn.Name, string(argsJSON))
		if err != nil {
			return nil, fmt.Errorf("WASM execution failed for %s.%s: %w", contract.ID, fn.Name, err)
		}
		return result, nil
	}

	// 3. Fallback: in-process state simulation
	if err := meter.Consume(GasPerSimOp); err != nil {
		return nil, err
	}
	return ce.simulateExecution(contract, fn, args)
}

// simulateExecution provides deterministic in-process execution when the
// WASM runtime is unavailable. It tracks contract state (balances, votes, escrows)
// so the contract engine remains useful for testing and non-WASM deployments.
func (ce *ContractEngine) simulateExecution(contract *Contract, fn *Function, args map[string]interface{}) (interface{}, error) {
	switch fn.Name {
	case "transfer":
		to, _ := args["to"].(string)
		amount, _ := toFloat64(args["amount"])
		if to == "" {
			return nil, fmt.Errorf("transfer: 'to' is required")
		}
		balKey := "balance:" + to
		cur, _ := toFloat64(contract.State[balKey])
		contract.State[balKey] = cur + amount
		ce.Events.Emit(contract.ID, "Transfer", map[string]interface{}{"to": to, "amount": amount, "new_balance": cur + amount}, 0)
		return map[string]interface{}{"success": true, "to": to, "amount": amount, "new_balance": cur + amount}, nil

	case "balanceOf":
		addr, _ := args["address"].(string)
		bal, _ := toFloat64(contract.State["balance:"+addr])
		return map[string]interface{}{"balance": bal}, nil

	case "vote":
		proposal, _ := args["proposal"].(string)
		choice, _ := args["choice"].(bool)
		if choice {
			v, _ := toFloat64(contract.State["yes:"+proposal])
			contract.State["yes:"+proposal] = v + 1
		} else {
			v, _ := toFloat64(contract.State["no:"+proposal])
			contract.State["no:"+proposal] = v + 1
		}
		ce.Events.Emit(contract.ID, "VoteCast", map[string]interface{}{"proposal": proposal, "choice": choice}, 0)
		return map[string]interface{}{"success": true, "proposal": proposal, "choice": choice}, nil

	case "getResults":
		proposal, _ := args["proposal"].(string)
		yes, _ := toFloat64(contract.State["yes:"+proposal])
		no, _ := toFloat64(contract.State["no:"+proposal])
		return map[string]interface{}{"yes": yes, "no": no}, nil

	case "deposit":
		amount, _ := toFloat64(args["amount"])
		escrowID := fmt.Sprintf("escrow_%d", len(contract.State))
		contract.State[escrowID] = map[string]interface{}{"amount": amount, "status": "held"}
		ce.Events.Emit(contract.ID, "EscrowCreated", map[string]interface{}{"escrow_id": escrowID, "amount": amount}, 0)
		return map[string]interface{}{"escrowId": escrowID, "amount": amount}, nil

	case "release":
		escrowID, _ := args["escrowId"].(string)
		entry, ok := contract.State[escrowID]
		if !ok {
			return nil, fmt.Errorf("escrow %s not found", escrowID)
		}
		if m, ok := entry.(map[string]interface{}); ok {
			m["status"] = "released"
		}
		ce.Events.Emit(contract.ID, "EscrowReleased", map[string]interface{}{"escrow_id": escrowID}, 0)
		return map[string]interface{}{"success": true}, nil

	case "refund":
		escrowID, _ := args["escrowId"].(string)
		entry, ok := contract.State[escrowID]
		if !ok {
			return nil, fmt.Errorf("escrow %s not found", escrowID)
		}
		if m, ok := entry.(map[string]interface{}); ok {
			m["status"] = "refunded"
		}
		ce.Events.Emit(contract.ID, "EscrowRefunded", map[string]interface{}{"escrow_id": escrowID}, 0)
		return map[string]interface{}{"success": true}, nil

	default:
		return map[string]interface{}{
			"function": fn.Name,
			"args":     args,
			"contract": contract.ID,
			"mode":     "simulated",
		}, nil
	}
}

func toFloat64(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case nil:
		return 0, true
	default:
		return 0, false
	}
}

// GetContract returns a contract by ID
func (ce *ContractEngine) GetContract(contractID string) (*Contract, error) {
	ce.mu.RLock()
	defer ce.mu.RUnlock()

	contract, exists := ce.contracts[contractID]
	if !exists {
		return nil, fmt.Errorf("contract %s not found", contractID)
	}

	return contract, nil
}

// ListContracts returns all deployed contracts
func (ce *ContractEngine) ListContracts() []*Contract {
	ce.mu.RLock()
	defer ce.mu.RUnlock()

	contracts := make([]*Contract, 0, len(ce.contracts))
	for _, contract := range ce.contracts {
		contracts = append(contracts, contract)
	}

	return contracts
}

// GetExecutionResult returns an execution result by ID
func (ce *ContractEngine) GetExecutionResult(executionID string) (*ExecutionResult, error) {
	ce.mu.RLock()
	defer ce.mu.RUnlock()

	result, exists := ce.executions[executionID]
	if !exists {
		return nil, fmt.Errorf("execution %s not found", executionID)
	}

	return result, nil
}

