package compilefixture
import ("ponder.local/ponder-go/orm/testdata/strict";"ponder.local/ponder-go/orm/testdata/nullable")
func check(){ _ = strict.Account; model := nullable.Account; _ = model; model = nullable.Account }
