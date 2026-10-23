# pg_query_go v5.1.0 local patch

Source module: `github.com/pganalyze/pg_query_go/v5@v5.1.0`, copied from the downloaded Go module cache. The complete module and its MIT/PostgreSQL license files are preserved.

`parser/src_port_snprintf.c` defines a static fallback `strchrnul` when `HAVE_STRCHRNUL` is absent. macOS SDK 15.4 and newer declare a libc function with the same name, causing `static declaration of 'strchrnul' follows non-static declaration` during the unmodified dependency build on this Mac.

Under the existing `#ifndef HAVE_STRCHRNUL` branch, this copy defines `strchrnul` as `pg_query_strchrnul`. This keeps the same fallback implementation and call site, while avoiding the system declaration collision. No parser grammar, SQL AST, validation rules or upstream test source was changed. The replacement is scoped through `go.mod`; system SDK files and global module cache contents were not edited.
