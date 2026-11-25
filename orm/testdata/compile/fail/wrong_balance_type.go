package compilefixture
import ("github.com/ysh0566/ponder-go/orm";"github.com/ysh0566/ponder-go/orm/testdata/strict")
func check(){ _ = orm.Set(1); _ = strict.AccountUpdate{Balance:orm.Set("20")} }
