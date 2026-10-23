package compilefixture
import ("math/big";"ponder.local/ponder-go/orm/testdata/strict")
func check(){ _ = big.NewInt(10); _ = strict.NewAccountInsert("0x0000000000000000000000000000000000000000",*big.NewInt(10)) }
