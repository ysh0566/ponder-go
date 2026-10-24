package main
import("ponder.local/ponder-go/filter";"ponder.local/ponder-go/bloom")
func main(){
 f:=filter.LogFilter{ChainID:1,SourceID:"native-bloom-types",Address:[]string{"0xef2d6d194084c2de36e0dabfce45d046b37d1106"},Topic0:filter.HexTopic("0x02c69be41d0b7e40352fc85be1cd65eb03d40ef8427a0ca4596b1ead9a00e9fc")}
 compiled,err:=bloom.GetLogFilterBloom(f)
 if err!=nil{panic(err)}
 if len(compiled.Topics[0])!=1||f.Object()["topic0"]!=string(f.Topic0){panic("typed runtime projection")}
}
