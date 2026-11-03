# v11 versus v10 selected native test coverage

v10 was the frozen factory-log milestone at a7cbf53b4ff7d79f1def47fceb88634b8ac1ec23.
v11 was factory-account transactions at 6d2b88ac6b6843ed4b5d650ec278a78207d2329b.
These are different input snapshots. No earlier pass is transferred.

| Native Package/Test set | Pure | PostgreSQL | PGlite | Total |
| --- | ---: | ---: | ---: | ---: |
| v10 selected unique leaves |249|190|142|581|
| Selected again and rerun on v11 |242|64|27|333|
| v10 leaves not rerun on v11 |7|126|115|248|
| Added to v11 selection |167|14|8|189|
| v11 selected unique leaves |409|78|35|522|

The 189 added selections comprise 159 existing events regressions and 30 new
native supplements (8 pure, 14 PostgreSQL, 8 PGlite). All previous 51 factory-specific
supplements are selected again. Exact package/test names, baseline groups and
commands are retained in the adjacent V11-V10-COVERAGE.json, copied byte-for-byte
from the sealed v11 evidence. Its original SHA256 is recorded below.

v11 accepted 48 groups and 699 executions: 522 unique leaves plus 177 race repeats,
which add zero unique credit. The original combined PG race attempt exhausted
its 1050-second outer budget after a cold race build and 13 passes; its missing
uncached ack did not receive pass credit. Disjoint 8+6 groups reran all 14 leaves on
the same frozen input at unchanged budgets and passed. Original failed logs and
receipt, including process_group_gone=false, remain in the v11 evidence.

v11 evidence remains read-only at:
/root/repo/ponder-go/m45-factory-extended-z4-20261102T193052Z-o71nm_um.

Neither selection is complete repository/full-race or full M45 acceptance.
Original-source mapping stays 543/905; supplements and repeats add no mapping credit.

Coverage JSON SHA256: 6be19a0bf5796c2c0c0599759bb4c9d01322556bd0c4e17d604833cf9fa27756.
