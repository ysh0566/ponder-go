package indexing

import (
	"encoding/hex"
	"fmt"
	"maps"

	ethabi "github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

// DeploylessCall runs a contract without deploying it on chain. Supply its
// creation code, or a factory address and calldata that will create the target.
type DeploylessCall struct {
	Code        string
	Factory     string
	FactoryData string
}

func (d DeploylessCall) transaction(tx Object) (Object, error) {
	if _, ok := tx["input"]; ok {
		return nil, fmt.Errorf("use data for deployless call input")
	}
	dataHex, ok := tx["data"].(string)
	if !ok {
		return nil, fmt.Errorf("deployless call requires hexadecimal calldata")
	}
	data, err := hexutil.Decode(dataHex)
	if err != nil {
		return nil, err
	}
	bytesType, err := ethabi.NewType("bytes", "", nil)
	if err != nil {
		return nil, err
	}
	b := ethabi.Argument{Type: bytesType}
	var prefix string
	var args []byte
	if d.Code != "" {
		if d.Factory != "" || d.FactoryData != "" || tx["to"] != nil {
			return nil, fmt.Errorf("creation code cannot be combined with a target or factory")
		}
		code, err := hexutil.Decode(d.Code)
		if err != nil {
			return nil, err
		}
		prefix = deploylessCodeWrapper
		args, err = (ethabi.Arguments{b, b}).Pack(code, data)
		if err != nil {
			return nil, err
		}
	} else {
		factory, ok := contractAddress(d.Factory)
		if !ok {
			return nil, fmt.Errorf("invalid deployless factory %q", d.Factory)
		}
		to, ok := contractAddress(tx["to"])
		if !ok {
			return nil, fmt.Errorf("deployless factory call requires a target address")
		}
		factoryData, err := hexutil.Decode(d.FactoryData)
		if err != nil {
			return nil, fmt.Errorf("invalid factory calldata: %w", err)
		}
		addressType, err := ethabi.NewType("address", "", nil)
		if err != nil {
			return nil, err
		}
		a := ethabi.Argument{Type: addressType}
		prefix = deploylessFactoryWrapper
		args, err = (ethabi.Arguments{a, b, a, b}).Pack(to, data, factory, factoryData)
		if err != nil {
			return nil, err
		}
	}
	request := maps.Clone(tx)
	delete(request, "to")
	request["data"] = prefix + hex.EncodeToString(args)
	return request, nil
}
