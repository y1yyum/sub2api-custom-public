package dispatch

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/custom/scheduledowner"
)

type ScheduledPlanSource interface {
	Schedule(context.Context, ScheduledConfig) (int, error)
}

type ScheduledSource struct {
	db         *sql.DB
	store      *scheduledowner.Store
	databaseID string
}

func NewScheduledSource(ctx context.Context, db *sql.DB, cfg ScheduledConfig) (*ScheduledSource, error) {
	normalized, err := normalizeScheduledConfig(cfg)
	if err != nil || db == nil {
		return nil, errors.New("invalid dispatch scheduled source")
	}
	source := &ScheduledSource{db: db, store: scheduledowner.NewStore(db).ForDatabase(normalized.DatabaseID), databaseID: normalized.DatabaseID}
	if err := source.checkPrivileges(ctx); err != nil {
		return nil, err
	}
	if err := source.store.CheckSchema(ctx); err != nil {
		return nil, errors.New("dispatch scheduled schema or database identity unavailable")
	}
	return source, nil
}

func (s *ScheduledSource) checkPrivileges(ctx context.Context) error {
	var unsafe bool
	err := s.db.QueryRowContext(ctx, `SELECT
	 EXISTS(SELECT 1 FROM pg_roles p WHERE pg_has_role(current_user,p.oid,'MEMBER')
	  AND (p.rolsuper OR p.rolcreaterole OR p.rolcreatedb OR p.rolbypassrls))
	 OR pg_has_role(current_user,'pg_read_all_data','MEMBER')
	 OR pg_has_role(current_user,'pg_write_all_data','MEMBER')
	 OR pg_has_role(current_user,'pg_read_server_files','MEMBER')
	 OR pg_has_role(current_user,'pg_write_server_files','MEMBER')
	 OR pg_has_role(current_user,'pg_execute_server_program','MEMBER')
	 OR EXISTS(SELECT 1 FROM pg_database d WHERE d.datname=current_database() AND
	  (pg_has_role(current_user,d.datdba,'MEMBER') OR has_database_privilege(current_user,d.oid,'CREATE')))
	 OR EXISTS(SELECT 1 FROM pg_namespace n WHERE n.nspname NOT LIKE 'pg_%' AND n.nspname<>'information_schema'
	  AND (pg_has_role(current_user,n.nspowner,'MEMBER') OR has_schema_privilege(current_user,n.oid,'CREATE')))
	 OR NOT has_schema_privilege(current_user,'scheduled_owner','USAGE')
	 OR EXISTS(SELECT 1 FROM (VALUES ('plan_id'),('slot'),('epoch'),('owner'),('state'),('plan_digest')) required(name)
	  WHERE NOT has_column_privilege(current_user,'scheduled_owner.queue',required.name,'INSERT'))
	 OR EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
	  WHERE n.nspname NOT LIKE 'pg_%' AND n.nspname<>'information_schema' AND c.relkind IN ('r','p','v','m','f')
	   AND (pg_has_role(current_user,c.relowner,'MEMBER')
	    OR has_table_privilege(current_user,c.oid,'UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
	    OR has_any_column_privilege(current_user,c.oid,'UPDATE,REFERENCES')
	    OR (c.oid<>'scheduled_owner.queue'::regclass AND
	     (has_table_privilege(current_user,c.oid,'INSERT') OR has_any_column_privilege(current_user,c.oid,'INSERT')))
	    OR (NOT c.oid=ANY(ARRAY[
	      'scheduled_owner.database_identity'::regclass,'scheduled_owner.schema_migrations'::regclass,
	      'scheduled_owner.plans'::regclass,'scheduled_owner.runs'::regclass,'scheduled_owner.queue'::regclass,
	      'public.scheduled_test_plans'::regclass,'public.scheduled_test_plan_questions'::regclass,
	      'public.intelligence_questions'::regclass]::oid[])
	     AND has_any_column_privilege(current_user,c.oid,'SELECT'))))
	 OR EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
	  WHERE n.nspname NOT LIKE 'pg_%' AND n.nspname<>'information_schema' AND c.relkind='S'
	  AND has_sequence_privilege(current_user,c.oid,'USAGE,SELECT,UPDATE'))`).Scan(&unsafe)
	if err != nil || unsafe {
		return errors.New("dispatch scheduled source requires a restricted queue-only role")
	}
	return nil
}

func (s *ScheduledSource) Schedule(ctx context.Context, cfg ScheduledConfig) (int, error) {
	normalized, err := normalizeScheduledConfig(cfg)
	if err != nil || normalized.DatabaseID != s.databaseID {
		return 0, errors.New("dispatch scheduled database identity conflict")
	}
	if err := s.checkPrivileges(ctx); err != nil {
		return 0, err
	}
	bindings := make([]scheduledowner.Binding, len(normalized.Bindings))
	for i, binding := range normalized.Bindings {
		bindings[i] = scheduledowner.Binding{PlanID: binding.PlanID, Epoch: binding.Epoch}
	}
	return s.store.Schedule(ctx, bindings)
}
