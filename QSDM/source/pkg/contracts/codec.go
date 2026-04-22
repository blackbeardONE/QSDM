package contracts

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ABICodec handles type-safe encoding and decoding of contract call parameters
// based on the contract's ABI definition.
type ABICodec struct{}

// NewABICodec creates a new codec instance.
func NewABICodec() *ABICodec {
	return &ABICodec{}
}

// EncodeCall validates and encodes parameters for a contract function call.
// Returns the validated parameter map with correctly typed values.
func (c *ABICodec) EncodeCall(abi *ABI, funcName string, rawArgs map[string]interface{}) (map[string]interface{}, error) {
	if abi == nil {
		return rawArgs, nil
	}

	fn, err := c.findFunction(abi, funcName)
	if err != nil {
		return nil, err
	}

	encoded := make(map[string]interface{}, len(fn.Inputs))
	for _, param := range fn.Inputs {
		raw, ok := rawArgs[param.Name]
		if !ok {
			return nil, fmt.Errorf("missing required parameter: %s", param.Name)
		}
		val, err := c.coerceValue(raw, param.Type, param.Name)
		if err != nil {
			return nil, err
		}
		encoded[param.Name] = val
	}

	return encoded, nil
}

// DecodeOutput validates return values against the function's output ABI.
func (c *ABICodec) DecodeOutput(abi *ABI, funcName string, rawOutput map[string]interface{}) (map[string]interface{}, error) {
	if abi == nil {
		return rawOutput, nil
	}

	fn, err := c.findFunction(abi, funcName)
	if err != nil {
		return rawOutput, nil // non-strict: return raw if function not found
	}

	if len(fn.Outputs) == 0 {
		return rawOutput, nil
	}

	decoded := make(map[string]interface{}, len(fn.Outputs))
	for _, param := range fn.Outputs {
		raw, ok := rawOutput[param.Name]
		if !ok {
			continue
		}
		val, err := c.coerceValue(raw, param.Type, param.Name)
		if err != nil {
			decoded[param.Name] = raw // keep original on coerce failure
			continue
		}
		decoded[param.Name] = val
	}

	// Carry through any extra fields not in ABI
	for k, v := range rawOutput {
		if _, exists := decoded[k]; !exists {
			decoded[k] = v
		}
	}

	return decoded, nil
}

// ValidateABI checks an ABI for structural validity.
func (c *ABICodec) ValidateABI(abi *ABI) []string {
	var issues []string
	if abi == nil {
		return []string{"ABI is nil"}
	}

	seen := make(map[string]bool)
	for i, fn := range abi.Functions {
		if fn.Name == "" {
			issues = append(issues, fmt.Sprintf("function[%d]: empty name", i))
			continue
		}
		if seen[fn.Name] {
			issues = append(issues, fmt.Sprintf("duplicate function name: %s", fn.Name))
		}
		seen[fn.Name] = true

		for j, p := range fn.Inputs {
			if p.Name == "" {
				issues = append(issues, fmt.Sprintf("%s.inputs[%d]: empty param name", fn.Name, j))
			}
			if !isValidType(p.Type) {
				issues = append(issues, fmt.Sprintf("%s.inputs[%d]: unknown type %q", fn.Name, j, p.Type))
			}
		}
		for j, p := range fn.Outputs {
			if !isValidType(p.Type) {
				issues = append(issues, fmt.Sprintf("%s.outputs[%d]: unknown type %q", fn.Name, j, p.Type))
			}
		}
	}
	return issues
}

// ABIToJSON serialises an ABI to canonical JSON.
func ABIToJSON(abi *ABI) ([]byte, error) {
	return json.MarshalIndent(abi, "", "  ")
}

// ABIFromJSON deserialises an ABI from JSON.
func ABIFromJSON(data []byte) (*ABI, error) {
	var abi ABI
	if err := json.Unmarshal(data, &abi); err != nil {
		return nil, fmt.Errorf("invalid ABI JSON: %w", err)
	}
	return &abi, nil
}

func (c *ABICodec) findFunction(abi *ABI, name string) (*Function, error) {
	for i := range abi.Functions {
		if abi.Functions[i].Name == name {
			return &abi.Functions[i], nil
		}
	}
	return nil, fmt.Errorf("function %q not found in ABI", name)
}

func (c *ABICodec) coerceValue(raw interface{}, typeName, paramName string) (interface{}, error) {
	switch strings.ToLower(typeName) {
	case "string", "address":
		return coerceString(raw, paramName)
	case "uint64", "uint256", "int64", "int256":
		return coerceUint64(raw, paramName)
	case "float64", "decimal":
		return coerceFloat64(raw, paramName)
	case "bool":
		return coerceBool(raw, paramName)
	case "bytes", "bytes32":
		return coerceBytes(raw, paramName)
	default:
		return raw, nil // pass through for unknown types
	}
}

func coerceString(raw interface{}, name string) (string, error) {
	switch v := raw.(type) {
	case string:
		return v, nil
	case json.Number:
		return v.String(), nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	default:
		return fmt.Sprintf("%v", raw), nil
	}
}

func coerceUint64(raw interface{}, name string) (uint64, error) {
	switch v := raw.(type) {
	case float64:
		if v < 0 || v > math.MaxUint64 || v != math.Trunc(v) {
			return 0, fmt.Errorf("param %s: %f is not a valid uint64", name, v)
		}
		return uint64(v), nil
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return 0, fmt.Errorf("param %s: %v is not a valid integer", name, v)
		}
		return uint64(n), nil
	case string:
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("param %s: %q is not a valid uint64", name, v)
		}
		return n, nil
	case int:
		return uint64(v), nil
	case int64:
		return uint64(v), nil
	case uint64:
		return v, nil
	default:
		return 0, fmt.Errorf("param %s: cannot convert %T to uint64", name, raw)
	}
}

func coerceFloat64(raw interface{}, name string) (float64, error) {
	switch v := raw.(type) {
	case float64:
		return v, nil
	case json.Number:
		return v.Float64()
	case string:
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return 0, fmt.Errorf("param %s: %q is not a valid float", name, v)
		}
		return f, nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	default:
		return 0, fmt.Errorf("param %s: cannot convert %T to float64", name, raw)
	}
}

func coerceBool(raw interface{}, name string) (bool, error) {
	switch v := raw.(type) {
	case bool:
		return v, nil
	case string:
		return strconv.ParseBool(v)
	case float64:
		return v != 0, nil
	default:
		return false, fmt.Errorf("param %s: cannot convert %T to bool", name, raw)
	}
}

func coerceBytes(raw interface{}, name string) (interface{}, error) {
	switch v := raw.(type) {
	case string:
		return v, nil // hex string
	case []byte:
		return v, nil
	default:
		return raw, nil
	}
}

var validTypes = map[string]bool{
	"string": true, "address": true,
	"uint64": true, "uint256": true, "int64": true, "int256": true,
	"float64": true, "decimal": true,
	"bool": true,
	"bytes": true, "bytes32": true,
}

func isValidType(t string) bool {
	return validTypes[strings.ToLower(t)]
}
