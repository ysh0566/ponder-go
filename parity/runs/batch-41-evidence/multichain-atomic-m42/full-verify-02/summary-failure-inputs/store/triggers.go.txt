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
		modeCheck = `IF TG_OP = 'DELETE' THEN chain_row := OLD.chain_id::text; ELSE chain_row := NEW.chain_id::text; END IF; IF chain_row::numeric IS DISTINCT FROM chain_value::numeric THEN RAISE EXCEPTION 'isolated write must match event chain'; END IF;`
	}
	definition := `CREATE OR REPLACE FUNCTION ` + function + `() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
 row_key_value jsonb := '{}'::jsonb;
 old_key jsonb;
 new_key jsonb;
 chain_row text;
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
 FOR i IN 0..TG_NARGS-1 LOOP
  IF TG_OP <> 'INSERT' THEN EXECUTE format('SELECT to_jsonb(($1).%I)',TG_ARGV[i]) INTO old_key USING OLD; END IF;
  IF TG_OP <> 'DELETE' THEN EXECUTE format('SELECT to_jsonb(($1).%I)',TG_ARGV[i]) INTO new_key USING NEW; END IF;
  IF TG_OP = 'UPDATE' AND old_key IS DISTINCT FROM new_key THEN RAISE EXCEPTION 'primary key updates are unsupported'; END IF;
  row_key_value := row_key_value || jsonb_build_object(TG_ARGV[i],CASE WHEN TG_OP='DELETE' THEN old_key ELSE new_key END);
 END LOOP;
 ` + modeCheck + `
 INSERT INTO ` + s.internal("_ponder_go_journal") + ` (table_name,row_key,before_row,row_data,operation,checkpoint,chain_id)
 VALUES (TG_TABLE_NAME,row_key_value,CASE WHEN TG_OP='INSERT' THEN NULL ELSE row_to_json(OLD) END,CASE WHEN TG_OP='INSERT' THEN row_to_json(NEW) ELSE row_to_json(OLD) END,CASE WHEN TG_OP='INSERT' THEN 0 WHEN TG_OP='UPDATE' THEN 1 ELSE 2 END,checkpoint_value,chain_value::numeric);
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

// This guard survives dropping capture triggers for maintenance. All public
// business DML still requires a journal, unless executing the controlled undo.
func (s *Store) createWriteGuards(ctx context.Context, tx Transaction) error {
	function := s.internal("_ponder_go_guard")
	definition := `CREATE OR REPLACE FUNCTION ` + function + `() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='TRUNCATE' THEN RAISE EXCEPTION 'TRUNCATE is unsupported for onchain tables'; END IF;
 IF current_setting('ponder_go.namespace',true) IS DISTINCT FROM TG_TABLE_SCHEMA THEN RAISE EXCEPTION 'business writes require an active indexing event'; END IF;
 IF current_setting('ponder_go.reverting',true) IS DISTINCT FROM '1' AND NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid=TG_RELID AND tgname='_ponder_go_capture' AND tgenabled<>'D') THEN RAISE EXCEPTION 'business writes require active journal triggers'; END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END; $$`
	if e := tx.Exec(ctx, definition); e != nil {
		return e
	}
	for _, table := range s.Schema.Tables {
		for _, trigger := range []struct{ name, event, level string }{{"_ponder_go_guard_row", "INSERT OR UPDATE OR DELETE", "ROW"}, {"_ponder_go_guard_truncate", "TRUNCATE", "STATEMENT"}} {
			if e := tx.Exec(ctx, "CREATE OR REPLACE TRIGGER "+schema.Quote(trigger.name)+" BEFORE "+trigger.event+" ON "+s.table(table.Name)+" FOR EACH "+trigger.level+" EXECUTE FUNCTION "+function+"()"); e != nil {
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
