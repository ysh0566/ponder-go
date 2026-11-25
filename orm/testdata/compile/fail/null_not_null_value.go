package compilefixture
import ("math/big";"github.com/ysh0566/ponder-go/orm/testdata/strict")
func check(){ _ = big.NewInt(10); _ = strict.NewAccountInsert("0x0000000000000000000000000000000000000000",nil) }
