package compilefixture
import ("github.com/ysh0566/ponder-go/orm/testdata/strict";"github.com/ysh0566/ponder-go/orm/testdata/nullable")
func check(){ _ = strict.Account; model := nullable.Account; _ = model; model = strict.Account }
