package compilefixture
import ("math/big";"github.com/ysh0566/ponder-go/orm";"github.com/ysh0566/ponder-go/orm/testdata/strict")
func check(){ ; _ = strict.AccountUpdate{Address:"0x0000000000000000000000000000000000000000",Balance:orm.Set(*big.NewInt(20))} }
