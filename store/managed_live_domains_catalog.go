package store

import "context"

// Include columns, defaults, constraints, indexes, triggers and extra reserved
// relations. Catalog changes are rejected before admitting either domain.
func liveDomainCatalog(ctx context.Context, q Querier, namespace string) (string, error) {
	return liveDomainCatalogFor(ctx, q, namespace, true)
}
func liveDomainCacheCatalog(ctx context.Context, q Querier, namespace string) (string, error) {
	return liveDomainCatalogFor(ctx, q, namespace, false)
}
func liveDomainCatalogFor(ctx context.Context, q Querier, namespace string, domainsOnly bool) (string, error) {
	queries := []string{
		`SELECT c.relname,c.relkind::text,c.relrowsecurity,c.relforcerowsecurity,a.attname,a.attnum::text,a.attnotnull,format_type(a.atttypid,a.atttypmod) AS type,pg_get_expr(ad.adbin,ad.adrelid) AS default FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace LEFT JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped LEFT JOIN pg_attrdef ad ON ad.adrelid=c.oid AND ad.adnum=a.attnum WHERE n.nspname=$1 AND (NOT $2::boolean OR left(c.relname,19)='_ponder_go_domains_') ORDER BY c.relname,a.attnum`,
		`SELECT c.relname,k.conname,pg_get_constraintdef(k.oid,true) AS definition FROM pg_constraint k JOIN pg_class c ON c.oid=k.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND (NOT $2::boolean OR left(c.relname,19)='_ponder_go_domains_') ORDER BY c.relname,k.conname`,
		`SELECT c.relname,i.relname AS index,pg_get_indexdef(i.oid) AS definition FROM pg_index x JOIN pg_class c ON c.oid=x.indrelid JOIN pg_class i ON i.oid=x.indexrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND (NOT $2::boolean OR left(c.relname,19)='_ponder_go_domains_') ORDER BY c.relname,i.relname`,
		`SELECT c.relname,t.tgname,t.tgenabled::text,pg_get_triggerdef(t.oid,true) AS definition,pg_get_functiondef(t.tgfoid) AS function_definition FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND (NOT $2::boolean OR left(c.relname,19)='_ponder_go_domains_') AND NOT t.tgisinternal ORDER BY c.relname,t.tgname`,
	}
	queries = append(queries, `SELECT p.proname,p.oid::regprocedure::text AS signature,pg_get_functiondef(p.oid) AS definition FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1 AND p.prokind IN ('f','p') AND (NOT $2::boolean OR left(p.proname,19)='_ponder_go_domains_') ORDER BY p.proname,p.oid::regprocedure::text`)
	rows := [][]map[string]any{}
	for _, sql := range queries {
		r, err := q.Query(ctx, sql, namespace, domainsOnly)
		if err != nil {
			return "", err
		}
		rows = append(rows, r.Rows)
	}
	return managedHash(rows), nil
}
