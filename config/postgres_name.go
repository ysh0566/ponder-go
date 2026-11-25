package config

import (
	"cmp"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

// DatabaseName returns the host:port/database used in source diagnostics.
// Authentication, TLS options and URL userinfo are excluded from the display name.
func (pool PostgresPoolConfig) DatabaseName() (string, error) {
	host, _ := pool["host"].(string)
	database, _ := pool["database"].(string)
	var port string
	if p := pool["port"]; p != nil {
		port = fmt.Sprint(p)
	}
	if connection, _ := pool["connectionString"].(string); connection != "" {
		var err error
		host, port, database, err = parseDatabaseLocation(connection)
		if err != nil {
			// net/url errors can include the full input and credentials.
			return "", errors.New("invalid PostgreSQL connection URL")
		}
	}
	return cmp.Or(host, os.Getenv("PGHOST"), "localhost") + ":" +
		cmp.Or(port, os.Getenv("PGPORT"), "5432") + "/" + cmp.Or(database, os.Getenv("PGDATABASE")), nil
}

func parseDatabaseLocation(connection string) (host, port, database string, err error) {
	// node-postgres also accepts a Unix-socket directory followed by a database name.
	if strings.HasPrefix(connection, "/") {
		parts := strings.SplitN(connection, " ", 3)
		if len(parts) > 1 {
			database = parts[1]
		}
		return parts[0], "", database, nil
	}
	// net/url rejects an escaped slash in the authority. PostgreSQL uses this
	// spelling for socket directories, e.g. postgres://%2Fvar%2Frun/db.
	var socketHost, encodedSocket string
	if scheme, rest, ok := strings.Cut(connection, "://"); ok {
		end := strings.IndexAny(rest, "/?#")
		if end < 0 {
			end = len(rest)
		}
		authority := rest[:end]
		hostname := authority[strings.LastIndexByte(authority, '@')+1:]
		if strings.HasPrefix(strings.ToLower(hostname), "%2f") {
			encoded, socketPort, hasPort := strings.Cut(hostname, ":")
			encodedSocket = encoded
			socketHost, err = url.PathUnescape(encoded)
			if err != nil {
				return "", "", "", err
			}
			replacement := "socket"
			if hasPort {
				replacement += ":" + socketPort
			}
			connection = scheme + "://" + strings.TrimSuffix(authority, hostname) + replacement + rest[end:]
		}
	}
	u, err := url.Parse(connection)
	if err != nil {
		return "", "", "", err
	}
	u = u.ResolveReference(&url.URL{})
	query := u.Query()
	path, err := decodeDatabasePath(u.EscapedPath())
	if err != nil {
		return "", "", "", err
	}
	if u.Scheme == "socket" {
		return path, "", query.Get("db"), nil
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return "", "", "", errors.New("unknown PostgreSQL URL scheme")
	}
	host = u.Hostname()
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if p := u.Port(); p != "" {
		number, err := strconv.ParseUint(p, 10, 16)
		if err != nil {
			return "", "", "", err
		}
		port = strconv.FormatUint(number, 10)
	}
	// node-postgres prefers the last query value over the URL authority.
	last := func(key string) string {
		if values := query[key]; len(values) > 0 {
			return values[len(values)-1]
		}
		return ""
	}
	if override := last("host"); override != "" {
		host = override
		if encodedSocket != "" {
			path = "/" + encodedSocket + path
		}
	} else if socketHost != "" {
		host = socketHost
	}
	return host, cmp.Or(last("port"), port), strings.TrimPrefix(path, "/"), nil
}

// JavaScript decodeURI leaves escaped URL delimiters intact inside names.
func decodeDatabasePath(path string) (string, error) {
	var name strings.Builder
	for i := 0; i < len(path); {
		if path[i] != '%' {
			name.WriteByte(path[i])
			i++
			continue
		}
		// EscapedPath supplies complete, validated escapes.
		escape := path[i : i+3]
		decoded, err := url.PathUnescape(escape)
		if err != nil {
			return "", err
		}
		if strings.Contains(";/?:@&=+$,#", decoded) {
			name.WriteString(escape)
		} else {
			name.WriteString(decoded)
		}
		i += 3
	}
	if !utf8.ValidString(name.String()) {
		return "", errors.New("invalid database name encoding")
	}
	return name.String(), nil
}
