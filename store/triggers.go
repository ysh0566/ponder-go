package store

import (
	"context"
	"fmt"
	"ponder.local/ponder-go/schema"
	"strings"
)

// All business DML, including raw SQL and writes through views, goes through the
// same database trigger. No Go write path maintains a second journal copy.
func (s *Store) createJournalTriggers(ctx context.Context, tx Transaction) error {
	function := s.internal("_ponder_go_capture")
	modeCheck := ""
	if s.Ordering == schema.Isolated {
		modeCheck = `IF (row_value->>'chain_id')::numeric IS DISTINCT FROM chain_value::numeric THEN RAISE EXCEPTION 'isolated write must match event chain'; END IF;`
	}
	definition := `CREATE OR REPLACE FUNCTION ` + function + `() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
 row_value jsonb;
 row_key_value jsonb := '{}'::jsonb;
 checkpoint_value text;
 chain_value text;
 i integer;
BEGIN
 IF current_setting('ponder_go.namespace',true) IS DISTINCT FROM TG_TABLE_SCHEMA THEN
  RAISE EXCEPTION 'business writes require an active indexing event';
 END IF;
 IF current_setting('ponder_go.reverting',true) = '1' THEN RETURN NULL; END IF;
 IF TG_OP = 'TRUNCATE' THEN RAISE EXCEPTION 'TRUNCATE is unsupported for onchain tables'; END IF;
 checkpoint_value := NULLIF(current_setting('ponder_go.checkpoint',true),'');
 chain_value := NULLIF(current_setting('ponder_go.chain_id',true),'');
 IF checkpoint_value IS NULL OR chain_value IS NULL THEN RAISE EXCEPTION 'business writes require checkpoint and chain context'; END IF;
 IF TG_OP = 'DELETE' THEN row_value := to_jsonb(OLD); ELSE row_value := to_jsonb(NEW); END IF;
 ` + modeCheck + `
 FOR i IN 0..TG_NARGS-1 LOOP
  IF TG_OP = 'UPDATE' AND (to_jsonb(OLD)->TG_ARGV[i]) IS DISTINCT FROM (to_jsonb(NEW)->TG_ARGV[i]) THEN RAISE EXCEPTION 'primary key updates are unsupported'; END IF;
  row_key_value := row_key_value || jsonb_build_object(TG_ARGV[i],row_value->TG_ARGV[i]);
 END LOOP;
 INSERT INTO ` + s.internal("_ponder_go_journal") + ` (table_name,row_key,before_row,checkpoint,chain_id)
 VALUES (TG_TABLE_NAME,row_key_value,CASE WHEN TG_OP='INSERT' THEN NULL ELSE to_jsonb(OLD) END,checkpoint_value,chain_value::numeric);
 RETURN NULL;
END;
$$`
	if e := tx.Exec(ctx, definition); e != nil {
		return e
	}
	for _, table := range s.Schema.Tables {
		keys := []string{}
		for _, key := range table.Keys() {
			keys = append(keys, "'"+strings.ReplaceAll(key, "'", "''")+"'")
		}
		for _, trigger := range []struct{ name, event, level string }{{"_ponder_go_capture", "INSERT OR UPDATE OR DELETE", "ROW"}, {"_ponder_go_no_truncate", "TRUNCATE", "STATEMENT"}} {
			if e := tx.Exec(ctx, "CREATE OR REPLACE TRIGGER "+schema.Quote(trigger.name)+" AFTER "+trigger.event+" ON "+s.table(table.Name)+" FOR EACH "+trigger.level+" EXECUTE FUNCTION "+function+"("+strings.Join(keys, ",")+")"); e != nil {
				return e
			}
		}
	}
	return nil
}
func (s *Store) eventContext(ctx context.Context, tx Transaction, checkpoint string, chain uint64, reverting bool) error {
	flag := "0"
	if reverting {
		flag = "1"
	}
	if e := tx.Exec(ctx, "SELECT set_config('ponder_go.namespace',$1,true),set_config('ponder_go.checkpoint',$2,true),set_config('ponder_go.chain_id',$3,true),set_config('ponder_go.reverting',$4,true)", s.Namespace, checkpoint, fmt.Sprint(chain), flag); e != nil {
		return e
	}
	return tx.Exec(ctx, "SET LOCAL search_path TO "+schema.Quote(s.Namespace)+",pg_catalog")
}
