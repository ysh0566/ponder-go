module ponder.local/ponder-go

go 1.25.0

require (
	github.com/ethereum/go-ethereum v1.16.3
	github.com/felixge/httpsnoop v1.0.4
	github.com/graphql-go/graphql v0.8.1
	github.com/jackc/pgx/v5 v5.7.6
	github.com/prometheus/client_golang v1.15.0
	github.com/prometheus/client_model v0.3.0
	github.com/prometheus/common v0.42.0
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/matttproud/golang_protobuf_extensions v1.0.4 // indirect
	github.com/prometheus/procfs v0.9.0 // indirect
	google.golang.org/protobuf v1.34.2 // indirect
)

require (
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.0.1 // indirect
	github.com/gorilla/websocket v1.5.3
	github.com/holiman/uint256 v1.3.2 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/pganalyze/pg_query_go/v5 v5.1.0
	golang.org/x/crypto v0.37.0 // indirect
	golang.org/x/sync v0.13.0 // indirect
	golang.org/x/sys v0.32.0 // indirect
	golang.org/x/text v0.24.0
)

replace github.com/pganalyze/pg_query_go/v5 => ./third_party/pg_query_go
