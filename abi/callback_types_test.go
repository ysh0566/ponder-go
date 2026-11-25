package abi

import (
	"math/big"
	"testing"
)

func TestCallbackWireTypes(t *testing.T) {
	for _, c := range []struct {
		params []Parameter
		event  bool
		want   string
	}{
		{[]Parameter{{Type: "uint256[2]", Indexed: true}}, true, "[1]abi.Hex"},
		{[]Parameter{{Type: "uint256[2]", Indexed: true}}, false, "[1][2]*big.Int"},
		{[]Parameter{{Type: "address"}, {Type: "uint256"}}, false, "struct {\nValue0 abi.Hex\nValue1 *big.Int\n}"},
		{[]Parameter{}, true, "[0]struct{}"},
		{[]Parameter{}, false, "abi.NoValue"},
	} {
		typ, err := CallbackArgsType(c.params, c.event)
		if err != nil || typ != c.want {
			t.Fatal(typ, err, c.want)
		}
	}
	type V struct {
		Owner  Hex      `abi:"owner"`
		Amount *big.Int `abi:"amount"`
	}
	n := big.NewInt(7)
	v, err := ProjectDecoded[V](map[string]any{"owner": "0x01", "amount": n})
	if err != nil || v.Owner != "0x01" || v.Amount.Int64() != 7 || v.Amount == n {
		t.Fatal(v, err)
	}
	if got, err := ProjectDecoded[V](map[string]any{"owner": "0x01", "amount": (*big.Int)(nil)}); err == nil || got != (V{}) {
		t.Fatal(got, err)
	}
}
