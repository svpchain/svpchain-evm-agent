package delegated

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"reflect"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// EVMContractMethodCall is the typed EVM-call surface. Contract is the
// configured lowercase address, rather than an alias, so the SVP-DT issuer can
// bind that exact address before it sends the request to this agent.
type EVMContractMethodCall struct {
	Contract string `json:"contract"`
	Method   string `json:"method"`
	Args     []any  `json:"args"`
	Value    string `json:"value,omitempty"`
}

type ExecEVMContractMethodInput struct {
	Proof []string              `json:"proof"`
	Call  EVMContractMethodCall `json:"call"`
}

// ExecuteEVMContractMethod encodes a configured ABI method and executes it
// through the existing MsgEVMCall path. It intentionally cannot call a method
// absent from the operator-maintained contract directory.
func (s *Service) ExecuteEVMContractMethod(ctx context.Context, in ExecEVMContractMethodInput) (ExecResult, error) {
	contract, ok := s.contracts[in.Call.Contract]
	if !ok {
		return ExecResult{}, fmt.Errorf("contract %q is not configured for typed delegated calls", in.Call.Contract)
	}
	data, err := encodeConfiguredMethod(contract, in.Call.Method, in.Call.Args)
	if err != nil {
		return ExecResult{}, err
	}
	return s.ExecuteEVMCall(ctx, ExecEVMCallInput{
		Proof: in.Proof,
		Call: EVMCallParams{
			Contract: contract.Address,
			Data:     "0x" + hex.EncodeToString(data),
			Value:    in.Call.Value,
		},
	})
}

func contractsByAddress(contracts []ConfiguredContract) map[string]ConfiguredContract {
	byAddress := make(map[string]ConfiguredContract, len(contracts))
	for _, contract := range contracts {
		byAddress[contract.Address] = contract
	}
	return byAddress
}

func encodeConfiguredMethod(contract ConfiguredContract, method string, values []any) ([]byte, error) {
	method = strings.TrimSpace(method)
	if !containsMethod(contract.Methods, method) {
		return nil, fmt.Errorf("method %q is not configured for contract %q", method, contract.ID)
	}
	name, types, err := parseMethodSignature(method)
	if err != nil {
		return nil, err
	}
	if len(values) != len(types) {
		return nil, fmt.Errorf("method %q expects %d arguments, got %d", method, len(types), len(values))
	}
	arguments := make(abi.Arguments, len(types))
	packedValues := make([]any, len(types))
	for i, typeName := range types {
		typ, err := abi.NewType(typeName, "", nil)
		if err != nil {
			return nil, fmt.Errorf("method %q argument %d type %q: %w", method, i, typeName, err)
		}
		arguments[i] = abi.Argument{Type: typ}
		value, err := abiValue(typ, values[i])
		if err != nil {
			return nil, fmt.Errorf("method %q argument %d: %w", method, i, err)
		}
		packedValues[i] = value
	}
	payload, err := arguments.Pack(packedValues...)
	if err != nil {
		return nil, fmt.Errorf("encode method %q: %w", method, err)
	}
	selector := crypto.Keccak256([]byte(name + "(" + strings.Join(types, ",") + ")"))[:4]
	return append(selector, payload...), nil
}

func containsMethod(methods []string, method string) bool {
	for _, configured := range methods {
		if strings.TrimSpace(configured) == method {
			return true
		}
	}
	return false
}

// parseMethodSignature accepts the conventional ABI function signature. Tuple
// components need ABI JSON, so reject them here rather than encoding a subtly
// different call from what the configuration describes.
func parseMethodSignature(signature string) (string, []string, error) {
	if strings.ContainsAny(signature, " \t\n") {
		return "", nil, fmt.Errorf("method signature %q must not contain whitespace", signature)
	}
	open := strings.IndexByte(signature, '(')
	if open <= 0 || !strings.HasSuffix(signature, ")") {
		return "", nil, fmt.Errorf("method signature %q must be name(type,...)", signature)
	}
	name := signature[:open]
	contents := signature[open+1 : len(signature)-1]
	if strings.ContainsAny(name, "()") || strings.ContainsAny(contents, "()") {
		return "", nil, fmt.Errorf("method signature %q uses unsupported tuple syntax", signature)
	}
	if contents == "" {
		return name, nil, nil
	}
	return name, strings.Split(contents, ","), nil
}

func abiValue(typ abi.Type, raw any) (any, error) {
	switch typ.T {
	case abi.AddressTy:
		text, ok := raw.(string)
		if !ok || !common.IsHexAddress(text) {
			return nil, fmt.Errorf("want EVM address string")
		}
		return common.HexToAddress(text), nil
	case abi.BoolTy:
		value, ok := raw.(bool)
		if !ok {
			return nil, fmt.Errorf("want boolean")
		}
		return value, nil
	case abi.StringTy:
		value, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("want string")
		}
		return value, nil
	case abi.BytesTy:
		return abiBytes(raw, -1)
	case abi.FixedBytesTy:
		bytes, err := abiBytes(raw, typ.Size)
		if err != nil {
			return nil, err
		}
		value := reflect.New(typ.GetType()).Elem()
		reflect.Copy(value, reflect.ValueOf(bytes))
		return value.Interface(), nil
	case abi.UintTy, abi.IntTy:
		return abiInteger(typ, raw)
	case abi.SliceTy, abi.ArrayTy:
		rawValues, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("want JSON array")
		}
		if typ.T == abi.ArrayTy && len(rawValues) != typ.Size {
			return nil, fmt.Errorf("want array of length %d, got %d", typ.Size, len(rawValues))
		}
		value := reflect.MakeSlice(reflect.SliceOf(typ.Elem.GetType()), len(rawValues), len(rawValues))
		if typ.T == abi.ArrayTy {
			value = reflect.New(typ.GetType()).Elem()
		}
		for i, rawValue := range rawValues {
			item, err := abiValue(*typ.Elem, rawValue)
			if err != nil {
				return nil, fmt.Errorf("element %d: %w", i, err)
			}
			value.Index(i).Set(reflect.ValueOf(item))
		}
		return value.Interface(), nil
	default:
		return nil, fmt.Errorf("ABI type %q is not supported by configured method calls", typ.String())
	}
}

func abiBytes(raw any, exactLength int) ([]byte, error) {
	text, ok := raw.(string)
	if !ok || !strings.HasPrefix(text, "0x") {
		return nil, fmt.Errorf("want 0x-prefixed hex bytes")
	}
	value, err := hex.DecodeString(text[2:])
	if err != nil {
		return nil, fmt.Errorf("decode hex bytes: %w", err)
	}
	if exactLength >= 0 && len(value) != exactLength {
		return nil, fmt.Errorf("want %d bytes, got %d", exactLength, len(value))
	}
	return value, nil
}

func abiInteger(typ abi.Type, raw any) (any, error) {
	text, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("want base-10 integer string")
	}
	value, ok := new(big.Int).SetString(text, 10)
	if !ok {
		return nil, fmt.Errorf("%q is not a base-10 integer", text)
	}
	if typ.T == abi.UintTy && value.Sign() < 0 {
		return nil, fmt.Errorf("unsigned integer must not be negative")
	}
	if typ.T == abi.UintTy && value.BitLen() > typ.Size {
		return nil, fmt.Errorf("integer exceeds uint%d", typ.Size)
	}
	if typ.T == abi.IntTy && value.BitLen() > typ.Size-1 {
		return nil, fmt.Errorf("integer exceeds int%d", typ.Size)
	}
	if typ.Size > 64 {
		return value, nil
	}
	target := typ.GetType()
	if typ.T == abi.UintTy {
		return reflect.ValueOf(value.Uint64()).Convert(target).Interface(), nil
	}
	return reflect.ValueOf(value.Int64()).Convert(target).Interface(), nil
}
