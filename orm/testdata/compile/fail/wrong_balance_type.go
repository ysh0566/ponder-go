package compilefixture
import ("ponder.local/ponder-go/orm";"ponder.local/ponder-go/orm/testdata/strict")
func check(){ _ = orm.Set(1); _ = strict.AccountUpdate{Balance:orm.Set("20")} }
