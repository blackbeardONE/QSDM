package contracts

import (
	"context"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/wasm"
)

// ContractTestFramework provides testing utilities for smart contracts
type ContractTestFramework struct {
	engine *ContractEngine
}

// NewContractTestFramework creates a new contract test framework
func NewContractTestFramework(wasmSDK *wasm.WASMSDK) *ContractTestFramework {
	return &ContractTestFramework{
		engine: NewContractEngine(wasmSDK),
	}
}

// DeployTestContract deploys a contract for testing
func (ctf *ContractTestFramework) DeployTestContract(ctx context.Context, templateName string) (*Contract, error) {
	template, err := GetTemplate(templateName)
	if err != nil {
		return nil, err
	}

	return ctf.engine.DeployContract(ctx, "test_"+templateName, template.Code, template.ABI, "test_owner")
}

// AssertExecutionSuccess asserts that a contract execution was successful
func (ctf *ContractTestFramework) AssertExecutionSuccess(t *testing.T, result *ExecutionResult, msg string) {
	if !result.Success {
		t.Errorf("%s: Expected success, got error: %s", msg, result.Error)
	}
}

// AssertExecutionFailure asserts that a contract execution failed
func (ctf *ContractTestFramework) AssertExecutionFailure(t *testing.T, result *ExecutionResult, msg string) {
	if result.Success {
		t.Errorf("%s: Expected failure, got success", msg)
	}
}

// AssertGasUsed asserts that gas usage is within expected range
func (ctf *ContractTestFramework) AssertGasUsed(t *testing.T, result *ExecutionResult, minGas, maxGas int64, msg string) {
	if result.GasUsed < minGas || result.GasUsed > maxGas {
		t.Errorf("%s: Gas used %d not in range [%d, %d]", msg, result.GasUsed, minGas, maxGas)
	}
}

// TestContractExecution tests contract execution with various scenarios
func (ctf *ContractTestFramework) TestContractExecution(t *testing.T, contractID string, functionName string, args map[string]interface{}) *ExecutionResult {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := ctf.engine.ExecuteContract(ctx, contractID, functionName, args)
	if err != nil {
		t.Fatalf("Failed to execute contract: %v", err)
	}

	return result
}

// BenchmarkContractExecution benchmarks contract execution performance
func (ctf *ContractTestFramework) BenchmarkContractExecution(b *testing.B, contractID string, functionName string, args map[string]interface{}) {
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ctf.engine.ExecuteContract(ctx, contractID, functionName, args)
	}
}

