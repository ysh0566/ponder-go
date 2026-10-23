package compilefixture
import ("math/big";"ponder.local/ponder-go/orm";"ponder.local/ponder-go/orm/testdata/strict")
func check(){ ; _ = strict.AccountUpdate{Balance:orm.Set(*big.NewInt(20))} }
