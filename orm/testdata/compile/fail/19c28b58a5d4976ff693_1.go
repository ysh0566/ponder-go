package compilefixture
import ("math/big";"ponder.local/ponder-go/orm";"ponder.local/ponder-go/orm/testdata/strict")
func check(){ ; _ = strict.AccountUpdate{Address:"0x0000000000000000000000000000000000000000",Balance:orm.Set(*big.NewInt(20))} }
