package indexing

import (
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common/hexutil"
)

// StateOverride supplies temporary account state for a simulated node call.
// An empty State map replaces all storage. StateDiff changes only listed slots.
type StateOverride map[string]AccountOverride

type AccountOverride struct {
	Balance   *big.Int
	Nonce     *uint64
	Code      *string
	State     map[string]string
	StateDiff map[string]string
}

func (s StateOverride) rpcParams() (Object, error) {
	if s == nil {
		return nil, nil
	}
	result := make(Object, len(s))
	for address, account := range s {
		a, ok := contractAddress(address)
		if !ok {
			return nil, fmt.Errorf("invalid state override address %q", address)
		}
		key := a.Hex()
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("duplicate state override for %s", key)
		}
		if account.State != nil && account.StateDiff != nil {
			return nil, fmt.Errorf("state and stateDiff cannot both be set for %s", key)
		}
		fields := Object{}
		if account.Balance != nil {
			if account.Balance.Sign() < 0 || account.Balance.BitLen() > 256 {
				return nil, fmt.Errorf("invalid balance override for %s", key)
			}
			fields["balance"] = hexutil.EncodeBig(account.Balance)
		}
		if account.Nonce != nil {
			fields["nonce"] = hexutil.EncodeUint64(*account.Nonce)
		}
		if account.Code != nil {
			fields["code"] = *account.Code
		}
		if account.State != nil {
			fields["state"] = account.State
		}
		if account.StateDiff != nil {
			fields["stateDiff"] = account.StateDiff
		}
		result[key] = fields
	}
	return result, nil
}
